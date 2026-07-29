-- +goose Up
-- Per-series pinned release group, a sort tier above profile score (issue #61).
-- Free text, deliberately not FK'd to profile groups: pinning an unlisted group
-- is the point. NULL means no pin.
ALTER TABLE series ADD COLUMN pinned_group TEXT;

-- +goose Down
ALTER TABLE series DROP COLUMN pinned_group;
