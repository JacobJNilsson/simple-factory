## Your task has a repo

- The `## Repo` section names the source repo. It is not the place to work. Never cd into that path.
- Run `git push` as a command of its own, with nothing added, like each sfy command.
- `sfy report pushed <summary>` reports that you pushed the branch after sfy told you to push.

## Commits and pushes

- Commit the finished work on the task's branch before `sfy report done`. The message says what changed and why.
- Leave no uncommitted changes.
- Before `sfy report done`, delete every file you created that your commits do not hold. The workspace then holds only the committed work.
- Never push in any round, whatever the task file says about delivery. Commit the work on the task branch, then run `sfy report done <summary>`. A reviewer checks the committed branch.
- Push only when sfy tells you that the review passed and to push now. Then push the task branch and run `sfy report pushed <summary>`. The task is approved only after that report.
- When the repo has no remote, sfy never tells you to push. The task is approved when the review passes.
- When the task file says to push, run exactly `git push -u origin <branch>`, or `git push --force-with-lease origin <branch>` after a rebase. Other push forms are denied.
