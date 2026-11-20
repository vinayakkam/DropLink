---
description: Start a DropLink task, e.g. /start-task p2/crypto Kashish or /start-task p1/ui-shell Vinayak
---

The person asked to start task: $ARGUMENTS

Follow the "Task protocol" in AGENTS.md exactly:
1. Confirm who the person is (Vinayak or Kashish) and that the task is theirs.
2. Read only the task file docs/tasks/<branch with / replaced by ->.md. Check every "Needs" branch exists or is merged.
3. Create the branch from the task's Base. Never push.
4. Do the task, touching only its listed files. Check it against "Done when" with `task check`.
5. Commit only: exactly 5 commits, the 4 feature steps from the task file then 1 test commit, each with a subject and a body saying what changed, why and how it was checked. No co-author or AI attribution.
6. Report the branch, every commit hash with its subject, and what you verified.
