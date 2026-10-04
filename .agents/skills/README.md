# Skills

`go/` and `go-spec-reviewer/` are vendored from [spf13/go-skills](https://github.com/spf13/go-skills)
(MIT, by Steve Francia — former Go team lead, author of Cobra/Viper/Hugo).

- `go` — idiomatic Go: package design, error handling, interfaces, concurrency, testing, modern stdlib. Loads automatically whenever Go code is written or reviewed.
- `go-spec-reviewer` — reviews a design/spec *before* implementation; catches over-engineering and non-idiomatic plans early.

To update, re-copy from the upstream repo, or install as a plugin instead:

```
/plugin marketplace add spf13/go-skills
/plugin install go@go-skills
```
