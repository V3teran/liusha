#!/usr/bin/env bash
# scripts/release/localize-notes.sh — 把 Release PR 发布说明里的英文标题「⚠ BREAKING CHANGES」改为中文。
#
# 这个标题写死在 release-please 的模板里，配置文件改不了（其余小节标题由 release-please-config.json
# 的 changelog-sections 指定）。release-please workflow 在 release-please 更新 Release PR 之后调用本脚本，
# 改两处：PR 正文（合并时 GitHub Release 的正文取自它）与 PR 分支上的 CHANGELOG.md。release-please 下次
# 更新 Release PR 会重新生成这两处，本脚本随之再改一次。已是中文时什么都不做。
#
# 前置：gh 已登录（GH_TOKEN）；在仓库检出里运行，git 能推送 Release PR 的分支且已设置提交身份。
set -euo pipefail

FROM='### ⚠ BREAKING CHANGES'
TO='### ⚠ 破坏性变更'

mapfile -t prs < <(gh pr list --state open --label 'autorelease: pending' \
  --json number,headRefName --jq '.[] | "\(.number) \(.headRefName)"')

for pr in "${prs[@]}"; do
  number="${pr%% *}" branch="${pr#* }"

  body="$(gh pr view "$number" --json body --jq .body)"
  if [[ "$body" == *"$FROM"* ]]; then
    printf '%s\n' "${body//"$FROM"/"$TO"}" | gh pr edit "$number" --body-file -
    echo "PR #$number：正文标题已改为中文"
  fi

  git fetch -q origin "$branch"
  git switch -q -C "$branch" "origin/$branch"
  if grep -qxF "$FROM" CHANGELOG.md; then
    perl -pi -e "s/^\Q$FROM\E\$/$TO/" CHANGELOG.md
    git commit -q -m 'chore: 发布说明的破坏性变更标题改为中文' -- CHANGELOG.md
    git push -q origin "$branch"
    echo "$branch：CHANGELOG.md 标题已改为中文"
  fi
done
