<!-- Thanks for contributing! Keep PRs focused; open an issue first for larger changes. -->

## What & why

<!-- What does this change, and what problem does it solve? Link any related issue. -->

Closes #

## Screenshots

<!-- Required for UI changes: before/after screenshots (or a short clip) of the
     affected screens, including dark mode if the change touches theming.
     Delete this section if the PR has no UI changes. -->

## Checklist

- [ ] `make lint` and `make test` pass locally
- [ ] Tests were written first (red → green): new/changed behaviour has coverage,
      and bug fixes include a test that failed before the fix
- [ ] UI changes include before/after screenshots above
- [ ] DB changes include a goose migration + queries + `make gen` (if applicable)
- [ ] New indexers / download clients / library targets go behind the existing interfaces
- [ ] No real scraped release names in test fixtures (use synthetic names)
- [ ] Docs updated (README / CONTRIBUTING) if behaviour or config changed
