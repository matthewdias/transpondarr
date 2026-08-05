package acquire

import (
	"encoding/json"
	"fmt"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// profileFromRows converts a stored profile and its ranked groups into the
// domain form the decide layer scores against.
func profileFromRows(p db.QualityProfile, groups []db.QualityProfileGroup) (domain.QualityProfile, error) {
	out := domain.QualityProfile{
		ID:              p.ID,
		Name:            p.Name,
		PreferredSource: p.PreferredSource,
		SubPref:         p.SubPref,
		PreferDualAudio: p.PreferDualAudio == 1,
		CodecPref:       p.CodecPref,
		MinScore:        int(p.MinScore),

		UpgradesEnabled:      p.UpgradesEnabled == 1,
		CutoffScore:          int(p.CutoffScore),
		UpgradeV2AboveCutoff: p.UpgradeV2AboveCutoff == 1,
	}
	if err := json.Unmarshal([]byte(p.ResolutionOrder), &out.ResolutionOrder); err != nil {
		return domain.QualityProfile{}, fmt.Errorf("profile %d resolution_order: %w", p.ID, err)
	}
	if err := json.Unmarshal([]byte(p.HardExcludes), &out.HardExcludes); err != nil {
		return domain.QualityProfile{}, fmt.Errorf("profile %d hard_excludes: %w", p.ID, err)
	}
	// ListProfileGroups orders unblocked rows by rank, so Groups stays ranked.
	for _, g := range groups {
		if g.Blocked == 1 {
			out.BlockedGroups = append(out.BlockedGroups, g.GroupName)
		} else {
			out.Groups = append(out.Groups, g.GroupName)
		}
	}
	return out, nil
}
