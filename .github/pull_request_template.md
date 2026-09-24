<!--
Small, focused pull requests are the ones most likely to be merged: see CONTRIBUTING.md.
If this is a larger change, link the issue where it was discussed.
-->

## What changed

<!-- What this does, kept to one thing. -->

## Why

<!-- The problem it solves, and why this is the right way to solve it. -->

## How it was checked

<!-- The tests you added or ran, the demo script, or what you did in the app. -->

## Screenshots

<!-- For anything visible in the app: before and after. For a flow or anything that moves: a short
     recording. For the rail or the sidebar: `npm --prefix desktop run preview -- --shots out --against main`.
     Delete this section if nothing on screen changed. -->

## Checklist

- [ ] `go vet ./...` and `go test ./...` pass
- [ ] `npm --prefix desktop run typecheck` and `npm --prefix desktop run build` pass
- [ ] Changed `internal/api`? Ran `UPDATE_TS=1 go test ./internal/api`
- [ ] Changed `internal/brief/brief.md.tmpl`? Ran `go test ./internal/brief -update`
- [ ] Changed `internal/image/provision.sh`? Bumped `image.Version`
- [ ] Changed `hubapi/`? Said so above: the hub needs the same change
- [ ] New migrations are appended, not edits to old ones
- [ ] Recorded the decision above, if the shape of something changed
