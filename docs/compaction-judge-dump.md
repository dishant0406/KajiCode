# Judge decision dump (keepThreshold=0.35)

Per-kind decision summary from the rebuilt judge over the four largest real
sessions, full raw transcripts, provider-free. Every candidate is one noul;
"kept" means `P(keep) ≥ 0.35`.

## zero_20260909101146_1788948706312871000_1

- candidates=1,394 · judge reclaim=1,089,388 tok · free prune=1,119,870 tok
- tool_result: kept=34 (39,269 tok) · dropped=254 (162,563 tok)
- tool_args: kept=9 (2,679 tok) · dropped=3 (768 tok)

## zero_20260908140107_1788876067019677000_336

- candidates=1,533 · judge reclaim=490,851 tok · free prune=518,559 tok
- tool_result: kept=22 (11,484 tok) · dropped=543 (301,363 tok)
- tool_args: kept=334 (132,613 tok) · dropped=43 (16,981 tok)

## zero_20260914101521_1789380921789735000_35

- candidates=1,185 · judge reclaim=476,161 tok · free prune=511,863 tok
- tool_result: kept=64 (55,812 tok) · dropped=777 (472,195 tok)
- tool_args: kept=282 (127,263 tok) · dropped=62 (23,065 tok)

## zero_20260903195423_1788465263836881000_39

- candidates=915 · judge reclaim=404,276 tok · free prune=426,376 tok
- tool_result: kept=35 (34,029 tok) · dropped=678 (405,985 tok)
- tool_args: kept=154 (70,454 tok) · dropped=48 (18,406 tok)

## Reading

- **Tool results:** the judge keeps a small (~1–5%) set of still-relevant bodies
  (the `read_file` / `Task` / `TaskOutput` / `todo_write` artifacts) and drops the
  bulk (old `bash`/`grep` dumps). This is exactly the relevance signal the blind
  age-based prune cannot produce.
- **Tool-call arguments:** the judge is more permissive — many argument sets are
  kept — which errs toward preserving content. Arguments are the class the prune
  never touches at all, so any reduction here is new reclaim.
- These are per-kind summaries; the earlier per-item dump (160 bodies, showing the
  precise `P(keep)` of each) was produced by the previous harness revision and is
  not regenerated because that harness has been removed.
