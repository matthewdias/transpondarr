# Writing style

How we write prose here: Markdown docs, Go comments, commit messages, CHANGELOG
entries and PR bodies. Code style is `CLAUDE.md`'s; this file governs the English.

## The axis is surprisal, not length

Sentence length doesn't predict difficulty. Readability formulas built on words
per sentence correlate below 0.3 with eye-tracking measures of reading effort,
and chopping sentences improves the score without improving comprehension. The
strongest predictor is surprisal: how much unexplained reference a reader has to
resolve.

So we don't shorten sentences. We make references resolvable, name things
literally, and put each example beside the claim it proves.

## Words

1. **Plain word over house metaphor, and over jargon that has a plain
   equivalent.** Not *load-bearing*, *blast radius*, *arm*, *gate*, *ladder*,
   *tier*, *floor*. Say important, impact, branch, check, escalating expiry,
   rank, minimum. A term of art goes the same way when a plain word exists:
   "deletes using rules" beats "deletes on a predicate". Keep the term where it
   names the specific thing rather than the general idea.
2. **No body or mind verbs for things that have neither.** A library doesn't hold
   a file; hands hold things. Name the literal operation instead: contains,
   stores, returns an error, doesn't match. This is the most common violation in
   the repo, so it's the first thing to check.
3. **Most ordinary construction.** Contractions are fine, and "doesn't use extra
   disk space" beats "uses no extra disk space". "instead of" beats "rather than"
   when the two sides are actions you're choosing between; keep "rather than" for
   a corrective identification, as in "length is the symptom rather than the
   test".
4. **Numerals and full forms.** Write "a 2-digit and 3-digit pair of episode
   numbers", not "a two- against three-digit pair". No suspended hyphens.
5. **Cut intensifiers and reflexives** — *at all*, *itself*, *simply*, *just*,
   *exactly*. Keep an adverb that adds information: *deliberately* means a person
   chose this.

## References

6. **Name the implied noun, and the implied action.** "a pair of episode
   numbers", not "a pair". "the grab row", not "the row". "another attempt at
   *writing* that destination", not "another attempt at that destination".
7. **Qualify a word this codebase uses twice.** The table below lists them. The
   identifiers already carry the qualifier and prose dropped it.
8. **Identifier when the sentence is about that code, description when it spans
   several.** `loadSweepItems` ANDs one column into one condition — name it. The
   search sweep spends one query per title — describe it, because that behaviour
   covers 13 identifiers and no single one means it. Pair the two at first use in
   a section, and anchor the identifier again when it returns after intervening
   material instead of assuming the first anchor still carries.
9. **Every section defines the terms it uses.** Docs get read alone, so a
   shorthand that a neighbouring paragraph explains is undefined here. Reuse the
   earlier wording instead of coining a synonym for it.
10. **Never point at something with a bare pronoun or demonstrative.** Not "is
    none of these" — say "doesn't match any of the three causes above". A
    sentence must not open with a pronoun standing for the whole of the sentence
    before it. Name the subject: "It is also why `staged` keys on a canonical
    path" becomes "`staged` keys on a canonical path".
11. **Name what an issue is about the first time you cite it.** A bare number
    carries no partial meaning, so "#170's question" tells a reader only that a
    question exists. "manual file adoption (#157)" needs nothing further.

## Claims

12. **Put the example beside the claim it proves**, inline as `(eg. …)`. A
    counterexample needs a different marker or it reads as support:
    `(but not E03/E30, which passes with the guard removed)`.
13. **State a rule's consequence, not its form.** Not "this generalises PR #57
    rather than listing the paths that exist today" — say "so file adoption
    (#157) inherits it without a new decision". A blanket "every one of these
    matters" goes the same way. Replace it with the specific defence of the one a
    reader would think is redundant, and delete it outright when the text already
    defends them one by one.
14. **State a constraint positively, name the actor, and give the purpose rather
    than the counterfactual.** "Only `acquire.passItem` can derive one field from
    the other" beats "nothing outside `acquire.passItem` may". "so that the
    registry doesn't silently fail to match" beats "because otherwise the
    registry silently fails to match".
15. **"We" beats an abstract noun as the subject.** "We can use that to solve
    this" beats "adoption will close it".
16. **Cut a sentence that could move to another project unchanged.** If it would
    read the same about any codebase, it's filler. Replace it with the fact,
    mechanism or consequence specific to this one. Every other rule here makes a
    sentence clear; this one makes it worth reading, and a rewrite that satisfies
    all of them can still smooth a load-bearing detail into generic clarity.
17. **Don't invent specifics that aren't load-bearing.** No durations, counts or
    examples added as texture — "a week-old payload" where any age would do. An
    example that instantiates the claim is different, and rule 12 asks for it.

## Shape

18. **Split a `, which …` clause that is a separate claim** into its own
    sentence. This isn't general chopping: a 35-word sentence making one point
    stays as it is.
19. **State a condition as "If X, …"** instead of as a participle hanging off the
    subject. Not "An agent working on the importer reads…" but "If an agent is
    working on the importer, it reads…".
20. **State a two-case rule with both cases together**, instead of asserting one
    and negating the other. "While file age protects a `.partial`, an `.upgrade`
    is protected by the in-flight registry instead" beats "the registry protects
    a live transfer, not the age" with the other half arriving three sentences
    later. A bare "not Y" leaves the reader holding a negation until Y's own rule
    turns up.
21. **List for three or more parallel items a reader checks independently. Table
    when each item has two or more attributes. Prose for a chain where each step
    depends on the one before.** Two items usually read fine as prose.
22. **Give a unit one subject.** A bullet or section answering one question can
    run long; one covering several needs a heading per subject whatever its
    length. Length is the symptom rather than the test — 400 words on one
    mechanism read fine, and 200 words across four don't. The signal is
    navigational: if a reader arriving with a specific question can't tell which
    paragraph answers it, the unit is carrying too many subjects. Treat this as
    something to moderate rather than a threshold to enforce.
23. **Median sentence about 24 words, ceiling about 33.** A guardrail, not a
    target — don't chop to reach it.
24. **The comment budget doesn't change: one line, why-only.** `CLAUDE.md` owns
    that rule. These rules govern the wording inside it.

## Words this codebase uses twice

Each of these names two or more unrelated things. The Go identifiers disambiguate
and prose has to as well, because a reader arriving cold at one sentence can't
tell which meaning is intended.

| Word | Meanings | Write |
|---|---|---|
| **title** | the tracked work (`Title`, `GetTitle`) · a release's name text (`ReleaseTitle`, `titleBelongs`) | the title · **the release name** |
| **grab** | the act (`AutoGrab`) · the record (`GrabID`, `failedGrab`) · the status (`grabbed`) | grab *(verb)* · **the grab row** · status `grabbed` |
| **row** | a `grabs` row · a `wanted_items` row | **grab row** · **item row** |
| **group** | the fansub group (`ReleaseGroup`) · a quality-profile group (`ProfileGroup`) · grab rows sharing an info hash (`grabGroup`) | **release group** · **profile group** · **info-hash group** |
| **sweep** | the wanted-search sweep (`SweepOnce`) · the staging-file sweep (`SweepStaging`) | **search sweep** · **staging sweep** |
| **page** | a paginated API response (`pageCursor`) · one indexer feed fetch (`pageParses`) · an AniList schedule page | **results page** · **feed page** · **schedule page** |
| **match** | release against title (`Match`) · file against item (`listUnmatched`) | **release match** · **file match** |
| **entry** | a feed entry (`FeedEntry`) · a blocklist entry (`BlocklistEntry`) · an AniList entry, which is a title (`seasonEntry`) | **feed entry** · **blocklist entry** · **AniList entry** |
| **cover** | a release covers an item (`covers`) · cover art (`CoverURL`) | **covers the item** · **cover art** |
| **state** | download state · search state · client state · breaker state | always qualify |
| **held** | the library already has a file (`held_release_title`) · the grab is delayed (`held_until`) | **already in the library** · **held until** |
| **walk** | a download's directory (`collectPayloadFiles`) · the ranked candidates (`walkCandidates`) | **payload walk** · **candidate walk** |
| **mode** | import mode · automation mode · auth required-mode · monitor mode | always qualify |
| **source** | which entry point (`passSource`) · release source, eg. BluRay or WEB (`PreferredSource`) · a file path (`SourcePath`) | **entry point** · **release source** · **source path** |
| **kind** | notification kind · item kind | **notification kind** · **item kind** |

`title` is the worst of these and the newest: #215 renamed the tracked work from
series to title, and "title" already meant a release's name string. The rename
covered Go identifiers, so the collision it introduced lives entirely in prose.

Checked and consistent, so they need nothing: item, reason, count, score, pin,
delay, name, key, format, queue, outcome, order.

## What review asks

Review is the check — we don't lint prose. So a reviewer produces a list rather
than a verdict, because "the style looks fine" isn't evidence.

**Quoted examples are exempt.** This file, and any doc explaining these rules,
quotes bad prose on purpose — *"a library doesn't hold a file"*, *"the feed owns
releases"*. Flag an author's own sentences, never an example cited as wrong, and
never the wordlists in the questions themselves.

**Cite rule numbers that exist here.** There are 24. An invented number is a
known failure of this kind of review.

Answer these in order, because the order is the priority. A quick pass answers
P0 and P1 and stops.

### P0 — the reader ends up missing something

1. **Claim preservation, for a rewrite.** Enumerate the claims in each rewritten
   paragraph before and after, then name every **fact about behaviour** that
   disappeared. Ask what a reader can no longer learn, not which sentences went.
   Rule 13 authorises deleting a claim that only states why the design has the
   shape it has, so a vanished sentence of that kind is the rule working rather
   than a loss. This is the check that matters most, and the only one that
   catches a correctness defect rather than a slow read. The two real failures so
   far were a dropped issue reference and an example attached to the wrong
   clause.
2. **Portability.** Quote any sentence that would read the same about another
   codebase, and name the specific fact it should carry instead.

### P1 — the reader ends up wrong

3. **Words used twice.** For every word in the table above, say which meaning is
   intended and whether a reader arriving cold at that sentence could tell.
4. **Unresolvable terms.** List every term the diff uses that a reader can't
   resolve from the diff itself or from a linked identifier.

### P2 — the reader ends up slow

5. **Body and mind verbs.** Quote every sentence where something without a body
   or a mind does a body or mind verb (holds, carries, owns, says, refuses,
   claims, remembers, wants, watches, travels, spends, escapes, reaches). Give
   the literal operation for each.
6. **House metaphors.** Quote every use of arm, gate, ladder, tier and floor, and
   give the plain word.
7. **Ceiling.** Quote any sentence over about 33 words.
