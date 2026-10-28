# AGENTS.md

Toolbox2Docker's fork of GitLab's fork of Docker Machine. Toolbox2Docker uses docker+machine as part of it's toolset.

## Changelog

`CHANGELOG.md` is the release record. Each `## v<version>` section is a release, newest first, and the newest section is the next release until it's tagged. See [Releases](README.md#releases).

An MR that changes what users get adds a bullet for it: a fix, a new or changed flag, environment variable or behaviour, or an update to Go or a Go module. An MR that only changes tests, CI or documentation doesn't.

- If the newest section isn't tagged yet, add the bullet to it.
- If it's tagged, start a new section at the top, `## v0.16.2-gitlab.<N+1>`, and set `Version` in `version/version.go` to `0.16.2-gitlab.<N+1>`.
- Never edit a section that's already tagged. That change would ship in no release.
- Write the bullet like the existing ones. Start with what changed (Add, Fix, Change, Update, Remove), say what a user sees or has to do, and end with `See <the MR's full URL>.`, for example `See https://gitlab.com/gitlab-org/ci-cd/docker-machine/-/merge_requests/206.`

`git ls-remote --tags origin 'v*'` lists the tags. The `release check` job fails an MR that gets this wrong, and says why.
