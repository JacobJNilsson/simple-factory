## Your task has a repo

- The `## Repo` section names the source repo. It is not the place to work. Never cd into that path.
- Run `git push` and `gh` as a command of its own, with nothing added, like each sfy command.
- `sfy report pushed <summary>` reports that you pushed the branch after sfy told you to push.

## Commits and pull requests

- Commit the finished work on the task's branch before `sfy report done`. The message says what changed and why.
- Leave no uncommitted changes. Do not push unless the task file says so.
- Before `sfy report done`, delete every file you created that your commits do not hold. The workspace then holds only the committed work.
- When the task file asks for a pull request, push the task branch and open the pull request.
- When feedback asks for changes, commit them on the task branch and do not push. Then run `sfy report done <summary>`. A reviewer checks the committed branch.
- Push only when sfy tells you that the review passed and to push now. Then run `sfy report pushed <summary>`. The task is approved only after that report.
- When the task's pull request is still open, push to the branch and open no second pull request. When it is merged or closed, or the task never had one, open a new pull request from the same branch.
- Open a pull request in two steps. Write its body to a file under `$TMPDIR`, never in your folder, then run `gh pr create --title '<title>' --body-file <file>`.
- When the task file says to push, run exactly `git push -u origin sfy/<task id>`, or `git push --force-with-lease origin sfy/<task id>` after a rebase. Other push forms are denied.
