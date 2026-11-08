#!/usr/bin/env sh
# 把构建产物上传到已有的 GitHub Release。同名附件会先删除再上传，支持重跑。
set -eu

REPO="dbkuaizi/kuai-net"
TAG="${CNB_BRANCH:-}"
if [ -z "$TAG" ]; then
  TAG="$(git describe --tags --exact-match 2>/dev/null || true)"
fi
if [ -z "$TAG" ]; then
  echo "Missing release tag" >&2
  exit 1
fi
if [ "$#" -eq 0 ]; then
  echo "No artifacts specified" >&2
  exit 1
fi

: "${GIT_ACCESS_TOKEN:?GIT_ACCESS_TOKEN is required}"

mkdir -p release

api="https://api.github.com/repos/${REPO}"
upload_api="https://uploads.github.com/repos/${REPO}"
auth_header="Authorization: Bearer ${GIT_ACCESS_TOKEN}"
accept_header="Accept: application/vnd.github+json"
version_header="X-GitHub-Api-Version: 2022-11-28"

curl -sS -f \
  -H "$auth_header" \
  -H "$accept_header" \
  -H "$version_header" \
  "${api}/releases/tags/${TAG}" \
  > release/github-release.json

release_id="$(jq -r '.id' release/github-release.json)"

for artifact in "$@"; do
  if [ ! -f "$artifact" ]; then
    echo "Artifact not found: ${artifact}" >&2
    exit 1
  fi

  name="$(basename "$artifact")"

  # 删掉同名旧附件，保证可重复执行
  curl -sS -f \
    -H "$auth_header" \
    -H "$accept_header" \
    -H "$version_header" \
    "${api}/releases/${release_id}/assets?per_page=100" \
    > release/github-assets.json

  jq -r --arg name "$name" '.[] | select(.name == $name) | .id' release/github-assets.json |
  while IFS= read -r asset_id; do
    [ -n "$asset_id" ] || continue
    curl -sS -f \
      -X DELETE \
      -H "$auth_header" \
      -H "$accept_header" \
      -H "$version_header" \
      "${api}/releases/assets/${asset_id}" \
      > /dev/null
  done

  curl -sS -f \
    -X POST \
    -H "$auth_header" \
    -H "$accept_header" \
    -H "$version_header" \
    -H "Content-Type: application/octet-stream" \
    --data-binary "@${artifact}" \
    "${upload_api}/releases/${release_id}/assets?name=${name}" \
    > "release/github-upload-${name}.json"

  jq -r '"GitHub asset uploaded: \(.browser_download_url)"' "release/github-upload-${name}.json"
done
