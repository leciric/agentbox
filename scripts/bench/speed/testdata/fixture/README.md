# benchfix

The repository AgentBox's speed benchmark (`scripts/bench/speed` in AgentBox)
runs its tasks on. It is generated, and its one commit is pinned: don't edit it
by hand.

- `strutil`: small string helpers.
- `stats`: summary statistics.
- `gen/pNN`: generated packages, there to make building and testing take a
  while; each mixes numbers through generated functions.
- `cmd/toolN`: programs built from the generated packages.

`./check.sh` vets, builds and tests everything from a cold build cache.

## strutil

- `Reverse(s)`: `s` with its runes in reverse order.
- `IsPalindrome(s)`: whether `s` reads the same backwards, ignoring case and
  punctuation.
- `Truncate(s, n)`: `s` cut to at most `n` runes, ending in "…" when cut.
- `CountWords(s)`: how many white-space-separated words `s` has.
