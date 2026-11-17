# Changelog Fragments

Use changelogger for user-visible changes:

```sh
changelogger
```

The command writes a changelog fragment under .changelogs and gives it a unique name.
Use the printed PR title so release automation can derive the intended version
bump.

Run `changelogger init --component appkit` to recreate this setup.

Release automation consumes these fragments after they are included in a
release.
