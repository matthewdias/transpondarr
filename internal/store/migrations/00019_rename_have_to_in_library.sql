-- +goose Up
-- The column and the derived item status share a name because one sources the
-- other (issue #84): renaming the status alone would split them and hide the
-- derivation. "in_library" also stays mechanism-agnostic, where "imported"
-- would name the importer as the only way an item can become held.
ALTER TABLE wanted_items RENAME COLUMN have TO in_library;

-- +goose Down
ALTER TABLE wanted_items RENAME COLUMN in_library TO have;
