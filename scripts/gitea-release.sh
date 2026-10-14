#!/bin/sh
# Publish a Gitea release for a tag and attach files to it.
#
#   GITEA_URL=https://gitea.example.com GITEA_REPO=owner/name GITEA_TOKEN=… \
#     sh scripts/gitea-release.sh v1.0.0 path/to/inks.dmg [more files…]
#
# The release notes are the tag's section of CHANGELOG.md. Re-running for
# the same tag reuses the release and replaces attachments with the same name.
set -eu
TAG=$1
shift
: "${GITEA_URL:?set GITEA_URL}" "${GITEA_REPO:?set GITEA_REPO (owner/name)}" "${GITEA_TOKEN:?set GITEA_TOKEN}"
cd "$(dirname "$0")/.."
API="${GITEA_URL%/}/api/v1/repos/$GITEA_REPO"
VERSION=${TAG#v}
auth="Authorization: token $GITEA_TOKEN"
json() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }

# Notes: the lines under "## <version>" up to the next "## " heading.
notes=$(awk -v v="$VERSION" '
  /^## / { if (found) exit; if (index($0, v)) { found = 1; next } }
  found { print }' CHANGELOG.md)
body=$(printf '%s' "$notes" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read().strip()))')

existing=$(curl -fsS -H "$auth" "$API/releases/tags/$TAG" 2>/dev/null || true)
id=""
[ -n "$existing" ] && id=$(printf '%s' "$existing" | json 'd["id"]')
if [ -z "$id" ]; then
  id=$(curl -fsS -X POST -H "$auth" -H "Content-Type: application/json" \
    -d "{\"tag_name\": \"$TAG\", \"name\": \"inks $VERSION\", \"body\": $body}" \
    "$API/releases" | json 'd["id"]')
  echo "Created release $TAG (id $id)"
else
  echo "Updating release $TAG (id $id)"
fi

for f in "$@"; do
  name=$(basename "$f")
  # Replace an attachment of the same name.
  old=$(curl -fsS -H "$auth" "$API/releases/$id/assets" | json "next((a['id'] for a in d if a['name'] == '$name'), '')")
  [ -n "$old" ] && curl -fsS -X DELETE -H "$auth" "$API/releases/$id/assets/$old" >/dev/null
  curl -fsS -X POST -H "$auth" -F "attachment=@$f" "$API/releases/$id/assets?name=$name" >/dev/null
  echo "Attached $name"
done
