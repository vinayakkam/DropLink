---
description: Start a DropLink task, e.g. /start-task p2/crypto Kashish or /start-task p1/ui-shell Vinayak
---

The person asked to start task: $ARGUMENTS

Follow the "Task protocol" in AGENTS.md exactly:
1. Confirm who the person is (Vinayak or Kashish) and that the task is theirs.
2. Read only the task file docs/tasks/<branch with / replaced by ->.md. Check every "Needs" branch is merged into main.
3. Create the branch from fresh main.
4. Do the task, touching only its listed files. Check it against "Done when" with `task check`.
5. Exactly 5 commits: the 4 feature steps from the task file, then 1 test commit, each with a subject and a body saying what changed, why and how it was checked. Kashish's work committed from Vinayak's machine ends with "Co-Authored-By: kashish1809 <kashishaternos@gmail.com>". Never any AI attribution.
6. When task check passes: push, open a PR to main and merge it (AGENTS.md step 8).
7. Report the branch, every commit hash with its subject, the PR link, and what you verified.
