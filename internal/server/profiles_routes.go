package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

type profileGroupDTO struct {
	Name    string `json:"name" doc:"Release group name; array order is the preference rank"`
	Blocked bool   `json:"blocked,omitempty" doc:"Never take this group, at any quality"`
}

type qualityProfileDTO struct {
	ID              int64             `json:"id"`
	Name            string            `json:"name"`
	IsDefault       bool              `json:"is_default"`
	ResolutionOrder []string          `json:"resolution_order"`
	PreferredSource string            `json:"preferred_source"`
	SubPref         string            `json:"sub_pref"`
	PreferDualAudio bool              `json:"prefer_dual_audio"`
	CodecPref       string            `json:"codec_pref"`
	HardExcludes    []string          `json:"hard_excludes"`
	MinScore        int64             `json:"min_score"`
	Groups          []profileGroupDTO `json:"groups"`
	TitleCount      int64             `json:"title_count" doc:"How many titles are assigned this profile"`

	UpgradesEnabled      bool  `json:"upgrades_enabled"`
	CutoffScore          int64 `json:"cutoff_score"`
	UpgradeV2AboveCutoff bool  `json:"upgrade_v2_above_cutoff"`
}

type profileBody struct {
	Name            string            `json:"name" required:"true" minLength:"1" maxLength:"120"`
	ResolutionOrder []string          `json:"resolution_order,omitempty" doc:"Best first, as height tiers like 1080p; an unlisted resolution scores zero"`
	PreferredSource string            `json:"preferred_source,omitempty" doc:"web, bd, tv or dvd; empty for no preference"`
	SubPref         string            `json:"sub_pref,omitempty" doc:"softsub or hardsub; empty for no preference"`
	PreferDualAudio bool              `json:"prefer_dual_audio,omitempty"`
	CodecPref       string            `json:"codec_pref,omitempty" doc:"h264, h265 or av1; empty for no preference"`
	HardExcludes    []string          `json:"hard_excludes,omitempty" doc:"Axis values a release must never carry: hardsub, softsub, h264, h265, av1, web, bd, tv, dvd, or a resolution like 1080p. Matched case-insensitively; unknown tokens are stored but never fire"`
	MinScore        int64             `json:"min_score,omitempty" minimum:"0" doc:"Floor: candidates scoring below are ineligible"`
	Groups          []profileGroupDTO `json:"groups,omitempty" doc:"Ranked group preference, most preferred first"`

	UpgradesEnabled      bool  `json:"upgrades_enabled,omitempty" doc:"Re-grab a held item while what holds it scores below the cutoff"`
	CutoffScore          int64 `json:"cutoff_score,omitempty" minimum:"0" doc:"Ceiling: a held release scoring at least this is good enough; zero means already met"`
	UpgradeV2AboveCutoff bool  `json:"upgrade_v2_above_cutoff,omitempty" doc:"Take the same group's v2/repack of what we hold even above the cutoff"`
}

type listProfilesOutput struct {
	Body struct {
		Profiles []qualityProfileDTO `json:"profiles"`
	}
}

type createProfileInput struct {
	Body profileBody
}

type updateProfileInput struct {
	ID   int64 `path:"id" doc:"Profile id"`
	Body profileBody
}

type profileOutput struct {
	Body qualityProfileDTO
}

type deleteProfileInput struct {
	ID         int64 `path:"id" doc:"Profile id"`
	ReassignTo int64 `query:"reassign_to" doc:"Profile to move this profile's titles to; required when the profile is in use"`
}

type assignTitleProfileInput struct {
	ID   int64 `path:"id" doc:"Title id"`
	Body struct {
		ProfileID int64 `json:"profile_id" required:"true"`
	}
}

type assignTitleProfileOutput struct {
	Body struct {
		TitleID   int64 `json:"title_id"`
		ProfileID int64 `json:"profile_id"`
	}
}

// profilesHandler owns the quality-profile CRUD plus the per-title assignment
// (registered here rather than with the title routes because its only logic is
// profile validity).
type profilesHandler struct {
	store *store.Store
}

func registerProfileRoutes(api huma.API, deps routeDeps) {
	h := &profilesHandler{store: deps.store}

	huma.Register(api, huma.Operation{
		OperationID: "list-quality-profiles",
		Method:      http.MethodGet,
		Path:        "/api/v1/profiles",
		Summary:     "List quality profiles with their ranked groups and usage counts",
		Tags:        []string{"profiles"},
	}, h.list)

	huma.Register(api, huma.Operation{
		OperationID:   "create-quality-profile",
		Method:        http.MethodPost,
		Path:          "/api/v1/profiles",
		Summary:       "Create a quality profile",
		Tags:          []string{"profiles"},
		DefaultStatus: http.StatusCreated,
	}, h.create)

	huma.Register(api, huma.Operation{
		OperationID: "update-quality-profile",
		Method:      http.MethodPut,
		Path:        "/api/v1/profiles/{id}",
		Summary:     "Update a quality profile, replacing its ranked groups",
		Tags:        []string{"profiles"},
	}, h.update)

	huma.Register(api, huma.Operation{
		OperationID:   "delete-quality-profile",
		Method:        http.MethodDelete,
		Path:          "/api/v1/profiles/{id}",
		Summary:       "Delete a quality profile, migrating its titles to reassign_to first",
		Tags:          []string{"profiles"},
		DefaultStatus: http.StatusNoContent,
	}, h.delete)

	huma.Register(api, huma.Operation{
		OperationID: "assign-title-profile",
		Method:      http.MethodPut,
		Path:        "/api/v1/titles/{id}/profile",
		Summary:     "Assign a quality profile to a title",
		Tags:        []string{"titles"},
	}, h.assignTitle)
}

var (
	sourceVocab = []string{"", "web", "bd", "tv", "dvd"}
	subVocab    = []string{"", "softsub", "hardsub"}
	codecVocab  = []string{"", "h264", "h265", "av1"}
)

// validate rejects axis values the parser can never produce — a profile naming
// them would silently never match (#14's "should not name axes the parser
// cannot fill"). hard_excludes and resolution_order stay unchecked, settled by
// #94 (wontfix): the UI must round-trip stored tokens it does not offer, and
// the resolution axis is open.
func validate(b profileBody) error {
	if strings.TrimSpace(b.Name) == "" {
		return errors.New("name must not be blank")
	}
	if !slices.Contains(sourceVocab, b.PreferredSource) {
		return fmt.Errorf("preferred_source %q is not one of web, bd, tv, dvd", b.PreferredSource)
	}
	if !slices.Contains(subVocab, b.SubPref) {
		return fmt.Errorf("sub_pref %q is not softsub or hardsub", b.SubPref)
	}
	if !slices.Contains(codecVocab, b.CodecPref) {
		return fmt.Errorf("codec_pref %q is not one of h264, h265, av1", b.CodecPref)
	}
	seen := map[string]bool{}
	for _, g := range b.Groups {
		name := strings.ToLower(strings.TrimSpace(g.Name))
		if name == "" {
			return errors.New("group names must be non-empty")
		}
		if seen[name] {
			return fmt.Errorf("group %q appears more than once", g.Name)
		}
		seen[name] = true
	}
	return nil
}

func jsonArray(vals []string) string {
	if vals == nil {
		vals = []string{}
	}
	b, _ := json.Marshal(vals)
	return string(b)
}

func profileDTO(p db.QualityProfile, groups []db.QualityProfileGroup, titleCount int64) (qualityProfileDTO, error) {
	out := qualityProfileDTO{
		ID:              p.ID,
		Name:            p.Name,
		IsDefault:       p.IsDefault == 1,
		PreferredSource: p.PreferredSource,
		SubPref:         p.SubPref,
		PreferDualAudio: p.PreferDualAudio == 1,
		CodecPref:       p.CodecPref,
		MinScore:        p.MinScore,
		Groups:          make([]profileGroupDTO, 0, len(groups)),
		TitleCount:      titleCount,

		UpgradesEnabled:      p.UpgradesEnabled == 1,
		CutoffScore:          p.CutoffScore,
		UpgradeV2AboveCutoff: p.UpgradeV2AboveCutoff == 1,
	}
	if err := json.Unmarshal([]byte(p.ResolutionOrder), &out.ResolutionOrder); err != nil {
		return out, fmt.Errorf("profile %d resolution_order: %w", p.ID, err)
	}
	if err := json.Unmarshal([]byte(p.HardExcludes), &out.HardExcludes); err != nil {
		return out, fmt.Errorf("profile %d hard_excludes: %w", p.ID, err)
	}
	for _, g := range groups {
		out.Groups = append(out.Groups, profileGroupDTO{Name: g.GroupName, Blocked: g.Blocked == 1})
	}
	return out, nil
}

// loadDTO assembles the full DTO for one profile row; the list endpoint reads in
// bulk instead, so this stays for create and update's post-commit re-read.
func (h *profilesHandler) loadDTO(ctx context.Context, p db.QualityProfile) (qualityProfileDTO, error) {
	groups, err := h.store.Q.ListProfileGroups(ctx, p.ID)
	if err != nil {
		return qualityProfileDTO{}, err
	}
	count, err := h.store.Q.CountTitlesByProfile(ctx, p.ID)
	if err != nil {
		return qualityProfileDTO{}, err
	}
	return profileDTO(p, groups, count)
}

// writeGroups replaces a profile's group rows; rank is the array position, so
// the client's order is the preference order.
func writeGroups(ctx context.Context, q *db.Queries, profileID int64, groups []profileGroupDTO) error {
	if err := q.DeleteProfileGroups(ctx, profileID); err != nil {
		return err
	}
	for i, g := range groups {
		blocked := int64(0)
		if g.Blocked {
			blocked = 1
		}
		if _, err := q.AddProfileGroup(ctx, db.AddProfileGroupParams{
			ProfileID: profileID,
			Rank:      int64(i + 1),
			GroupName: strings.TrimSpace(g.Name),
			Blocked:   blocked,
		}); err != nil {
			return err
		}
	}
	return nil
}

// requireNameFree applies to profile names the case-insensitive rule validate
// already applies to group names. Callers must skip it when the name is not
// changing, since a row can hold a name this refuses (see update).
func requireNameFree(ctx context.Context, q *db.Queries, name string) error {
	_, err := q.GetQualityProfileByName(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return huma.Error500InternalServerError("failed to check profile name", err)
	}
	return huma.Error409Conflict("a profile with that name already exists")
}

// Neither the create nor the update statement writes is_default, so name is the
// only unique constraint they can violate.
func isUniqueNameErr(err error) bool { return store.IsUniqueViolation(err) }

// The listing is unpaginated, so there is no id set to scope by (#91): one query
// for groups, one for counts, three whatever the profile count.
func (h *profilesHandler) list(ctx context.Context, _ *struct{}) (*listProfilesOutput, error) {
	rows, err := h.store.Q.ListQualityProfiles(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list profiles", err)
	}
	groupRows, err := h.store.Q.ListAllProfileGroups(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list profile groups", err)
	}
	countRows, err := h.store.Q.CountTitlesPerProfile(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to count series", err)
	}

	// The query orders by profile before rank, so appending in scan order leaves
	// each profile's groups ranked exactly as ListProfileGroups returns them.
	groups := map[int64][]db.QualityProfileGroup{}
	for _, g := range groupRows {
		groups[g.ProfileID] = append(groups[g.ProfileID], g)
	}
	counts := make(map[int64]int64, len(countRows))
	for _, c := range countRows {
		counts[c.QualityProfileID] = c.TitleCount
	}

	out := &listProfilesOutput{}
	out.Body.Profiles = make([]qualityProfileDTO, 0, len(rows))
	for _, p := range rows {
		dto, derr := profileDTO(p, groups[p.ID], counts[p.ID])
		if derr != nil {
			return nil, huma.Error500InternalServerError("failed to load profile", derr)
		}
		out.Body.Profiles = append(out.Body.Profiles, dto)
	}
	return out, nil
}

func (h *profilesHandler) create(ctx context.Context, in *createProfileInput) (*profileOutput, error) {
	if err := validate(in.Body); err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	tx, err := h.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to begin transaction", err)
	}
	defer func() { _ = tx.Rollback() }()
	qtx := h.store.Q.WithTx(tx)

	name := strings.TrimSpace(in.Body.Name)
	if nerr := requireNameFree(ctx, qtx, name); nerr != nil {
		return nil, nerr
	}

	row, err := qtx.CreateQualityProfile(ctx, db.CreateQualityProfileParams{
		Name:            name,
		ResolutionOrder: jsonArray(in.Body.ResolutionOrder),
		PreferredSource: in.Body.PreferredSource,
		SubPref:         in.Body.SubPref,
		PreferDualAudio: boolInt(in.Body.PreferDualAudio),
		CodecPref:       in.Body.CodecPref,
		HardExcludes:    jsonArray(in.Body.HardExcludes),
		MinScore:        in.Body.MinScore,

		UpgradesEnabled:      boolInt(in.Body.UpgradesEnabled),
		CutoffScore:          in.Body.CutoffScore,
		UpgradeV2AboveCutoff: boolInt(in.Body.UpgradeV2AboveCutoff),
	})
	if isUniqueNameErr(err) {
		return nil, huma.Error409Conflict("a profile with that name already exists")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to create profile", err)
	}
	if err := writeGroups(ctx, qtx, row.ID, in.Body.Groups); err != nil {
		return nil, huma.Error500InternalServerError("failed to write groups", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, huma.Error500InternalServerError("failed to commit", err)
	}

	dto, err := h.loadDTO(ctx, row)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load profile", err)
	}
	return &profileOutput{Body: dto}, nil
}

func (h *profilesHandler) update(ctx context.Context, in *updateProfileInput) (*profileOutput, error) {
	if err := validate(in.Body); err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	tx, err := h.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to begin transaction", err)
	}
	defer func() { _ = tx.Rollback() }()
	qtx := h.store.Q.WithTx(tx)

	// An unknown id is a 404, not a name clash, so existence is checked first.
	current, gerr := qtx.GetQualityProfile(ctx, in.ID)
	if errors.Is(gerr, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("profile not found")
	} else if gerr != nil {
		return nil, huma.Error500InternalServerError("failed to load profile", gerr)
	}
	// Only an actual rename is checked: an install predating this rule can hold
	// Anime and anime, and the second row's own lookup returns the first.
	name := strings.TrimSpace(in.Body.Name)
	if !strings.EqualFold(name, current.Name) {
		if nerr := requireNameFree(ctx, qtx, name); nerr != nil {
			return nil, nerr
		}
	}

	row, err := qtx.UpdateQualityProfile(ctx, db.UpdateQualityProfileParams{
		Name:            name,
		ResolutionOrder: jsonArray(in.Body.ResolutionOrder),
		PreferredSource: in.Body.PreferredSource,
		SubPref:         in.Body.SubPref,
		PreferDualAudio: boolInt(in.Body.PreferDualAudio),
		CodecPref:       in.Body.CodecPref,
		HardExcludes:    jsonArray(in.Body.HardExcludes),
		MinScore:        in.Body.MinScore,

		UpgradesEnabled:      boolInt(in.Body.UpgradesEnabled),
		CutoffScore:          in.Body.CutoffScore,
		UpgradeV2AboveCutoff: boolInt(in.Body.UpgradeV2AboveCutoff),

		ID: in.ID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("profile not found")
	}
	if isUniqueNameErr(err) {
		return nil, huma.Error409Conflict("a profile with that name already exists")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to update profile", err)
	}
	if err := writeGroups(ctx, qtx, row.ID, in.Body.Groups); err != nil {
		return nil, huma.Error500InternalServerError("failed to write groups", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, huma.Error500InternalServerError("failed to commit", err)
	}

	dto, err := h.loadDTO(ctx, row)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load profile", err)
	}
	return &profileOutput{Body: dto}, nil
}

func (h *profilesHandler) delete(ctx context.Context, in *deleteProfileInput) (*struct{}, error) {
	prof, err := h.store.Q.GetQualityProfile(ctx, in.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("profile not found")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load profile", err)
	}
	if prof.IsDefault == 1 {
		return nil, huma.Error422UnprocessableEntity("the default profile cannot be deleted")
	}

	count, err := h.store.Q.CountTitlesByProfile(ctx, in.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to count series", err)
	}
	if count > 0 && in.ReassignTo == 0 {
		return nil, huma.Error409Conflict(fmt.Sprintf(
			"profile is assigned to %d series; pass reassign_to with the profile to migrate them to", count))
	}

	tx, err := h.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to begin transaction", err)
	}
	defer func() { _ = tx.Rollback() }()
	qtx := h.store.Q.WithTx(tx)

	if count > 0 {
		if in.ReassignTo == in.ID {
			return nil, huma.Error422UnprocessableEntity("cannot reassign series to the profile being deleted")
		}
		if _, terr := qtx.GetQualityProfile(ctx, in.ReassignTo); errors.Is(terr, sql.ErrNoRows) {
			return nil, huma.Error422UnprocessableEntity("reassign_to profile does not exist")
		} else if terr != nil {
			return nil, huma.Error500InternalServerError("failed to load target profile", terr)
		}
		if rerr := qtx.ReassignTitleProfile(ctx, db.ReassignTitleProfileParams{
			QualityProfileID: in.ReassignTo, QualityProfileID_2: in.ID,
		}); rerr != nil {
			return nil, huma.Error500InternalServerError("failed to reassign series", rerr)
		}
	}
	rows, err := qtx.DeleteQualityProfile(ctx, in.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to delete profile", err)
	}
	if rows != 1 {
		return nil, huma.Error409Conflict("profile could not be deleted (still in use?)")
	}
	if err := tx.Commit(); err != nil {
		return nil, huma.Error500InternalServerError("failed to commit", err)
	}
	return nil, nil
}

func (h *profilesHandler) assignTitle(ctx context.Context, in *assignTitleProfileInput) (*assignTitleProfileOutput, error) {
	if _, err := h.store.Q.GetTitle(ctx, in.ID); errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("series not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("failed to load series", err)
	}
	rows, err := h.store.Q.SetTitleProfile(ctx, db.SetTitleProfileParams{
		QualityProfileID: in.Body.ProfileID,
		ID:               in.ID,
		ID_2:             in.Body.ProfileID,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to assign profile", err)
	}
	if rows == 0 {
		return nil, huma.Error422UnprocessableEntity("profile does not exist")
	}
	out := &assignTitleProfileOutput{}
	out.Body.TitleID = in.ID
	out.Body.ProfileID = in.Body.ProfileID
	return out, nil
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
