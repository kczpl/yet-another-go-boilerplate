# Skills

`go/` and `go-spec-reviewer/` are vendored from [spf13/go-skills](https://github.com/spf13/go-skills)
(MIT, by Steve Francia — former Go team lead, author of Cobra/Viper/Hugo).

- `go` — idiomatic Go: package design, error handling, interfaces, concurrency, testing, modern stdlib.
- `go-spec-reviewer` — reviews a design/spec *before* implementation; catches over-engineering and non-idiomatic plans early.

Only the `SKILL.md` files are vendored. The upstream plugin manifests
(`.claude-plugin/`) are left out: this repository keeps no agent-specific
files. Agents do not discover these skills automatically; `AGENTS.md` tells
them when to read each file.

To update, copy `SKILL.md` from the upstream repo. To get the skills as
auto-loaded skills in Claude Code, install the upstream plugin at user level
(this changes nothing in the repository):

```
/plugin marketplace add spf13/go-skills
/plugin install go@go-skills
```
