## Your task has a repo

- The `## Repo` section names the source repo. It is not the place to work. Never cd into that path.
- Run `git push` and `gh` as a command of its own, with nothing added, like each sfy command.
- `sfy report pushed <summary>` reports that you pushed the branch after sfy told you to push.

## Commits and pull requests

- Commit the finished work on the task's branch before `sfy report done`. The message says what changed and why.
- Leave no uncommitted changes.
- Before `sfy report done`, delete every file you created that your commits do not hold. The workspace then holds only the committed work.
- Never push in any round, whatever the task file says about delivery. Commit the work on the task branch, then run `sfy report done <summary>`. A reviewer checks the committed branch.
- Push only when sfy tells you that the review passed and to push now. Then push the task branch. When the task file asks for a pull request, open it too. Then run `sfy report pushed <summary>`. The task is approved only after that report.
- When the task's pull request is still open, push to the branch and open no second pull request. When it is merged or closed, or the task never had one, open a new pull request from the same branch.
- Open a pull request in three steps. Write its body to a file in your folder, for example `pr-body.md`, and never commit it.
- Run `gh pr create --title '<title>' --body-file <file>` as a command of its own. The same applies to `gh pr edit <number> --body-file <file>`.
- Delete the body file right after the gh command, before any report.
- When the task file says to push, run exactly `git push -u origin sfy/<task id>`, or `git push --force-with-lease origin sfy/<task id>` after a rebase. Other push forms are denied.
