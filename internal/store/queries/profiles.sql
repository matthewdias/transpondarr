-- NOTE: keep comments in this file ASCII-only. sqlc's sqlite codegen miscounts
-- byte vs. rune offsets and a multi-byte character in a doc comment silently
-- truncates the emitted SQL. See CLAUDE.md.

-- name: ListQualityProfiles :many
SELECT *
FROM quality_profiles
ORDER BY is_default DESC, name;

-- name: GetQualityProfile :one
SELECT *
FROM quality_profiles
WHERE id = ?
LIMIT 1;

-- name: GetDefaultQualityProfile :one
SELECT *
FROM quality_profiles
WHERE is_default = 1
LIMIT 1;

-- name: CreateQualityProfile :one
INSERT INTO quality_profiles (name, resolution_order, preferred_source, sub_pref, prefer_dual_audio, codec_pref, hard_excludes, min_score, upgrades_enabled, cutoff_score, upgrade_v2_above_cutoff)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateQualityProfile :one
UPDATE quality_profiles
SET name                    = ?,
    resolution_order        = ?,
    preferred_source        = ?,
    sub_pref                = ?,
    prefer_dual_audio       = ?,
    codec_pref              = ?,
    hard_excludes           = ?,
    min_score               = ?,
    upgrades_enabled        = ?,
    cutoff_score            = ?,
    upgrade_v2_above_cutoff = ?
WHERE id = ?
RETURNING *;

-- name: DeleteQualityProfile :execrows
DELETE FROM quality_profiles
WHERE quality_profiles.id = ? AND is_default = 0
  AND NOT EXISTS (SELECT 1 FROM series WHERE series.quality_profile_id = quality_profiles.id);

-- name: CountSeriesByProfile :one
SELECT COUNT(*)
FROM series
WHERE quality_profile_id = ?;

-- name: ListSeriesByProfile :many
SELECT id, title
FROM series
WHERE quality_profile_id = ?
ORDER BY title;

-- name: ReassignSeriesProfile :exec
UPDATE series
SET quality_profile_id = ?
WHERE quality_profile_id = ?;

-- name: SetSeriesProfile :execrows
UPDATE series
SET quality_profile_id = ?
WHERE series.id = ?
  AND EXISTS (SELECT 1 FROM quality_profiles WHERE quality_profiles.id = ?);

-- name: ListProfileGroups :many
SELECT *
FROM quality_profile_groups
WHERE profile_id = ?
ORDER BY blocked, rank, group_name;

-- name: AddProfileGroup :one
INSERT INTO quality_profile_groups (profile_id, rank, group_name, blocked)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: DeleteProfileGroups :exec
DELETE FROM quality_profile_groups
WHERE profile_id = ?;
