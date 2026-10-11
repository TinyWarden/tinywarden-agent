#!/usr/bin/env python3
"""Create a complete GitHub draft; publication is a reviewed final action."""
import argparse
import json
import os
import re
import urllib.parse
import urllib.request
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("--tag", required=True)
parser.add_argument("--directory", required=True)
args = parser.parse_args()
if not re.fullmatch(r"v(?:0|[1-9][0-9]{0,8})\.(?:0|[1-9][0-9]{0,8})\.(?:0|[1-9][0-9]{0,8})", args.tag):
    raise SystemExit("Invalid tag")
directory = Path(args.directory)
names = {"release.json", "release.json.sig", "SHA256SUMS", "install.sh",
         "tinywarden-agent-" + args.tag[1:] + "-linux-amd64.tar.gz"}
if not directory.is_dir() or {p.name for p in directory.iterdir()} != names:
    raise SystemExit("Release directory must contain exactly the complete assets")
token = os.environ.pop("GH_TOKEN")


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise RuntimeError("Unexpected GitHub API redirect")


opener = urllib.request.build_opener(NoRedirect)


def request(url, data, content_type="application/json"):
    req = urllib.request.Request(url, data=data, method="POST", headers={
        "Authorization": "Bearer " + token, "Content-Type": content_type,
        "Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28"})
    with opener.open(req, timeout=90) as response:
        body = response.read(2097153)
        if len(body) > 2097152:
            raise RuntimeError("Oversized GitHub response")
        return json.loads(body)


repo = "TinyWarden/tinywarden-agent"
release = request("https://api.github.com/repos/" + repo + "/releases", json.dumps({
    "tag_name": args.tag, "name": args.tag, "draft": True, "prerelease": False,
    "body": "Debian 13 x64 managed agent platform. See installation and upgrade documentation."}).encode())
release_id = release["id"]
if not isinstance(release_id, int) or release_id < 1:
    raise SystemExit("Invalid draft release response")
for path in sorted(directory.iterdir()):
    if not path.is_file() or path.is_symlink() or path.stat().st_size > 67108864:
        raise SystemExit("Invalid asset; release remains a draft")
    url = "https://uploads.github.com/repos/" + repo + "/releases/" + str(release_id) + "/assets?name=" + urllib.parse.quote(path.name)
    request(url, path.read_bytes(), "application/octet-stream")
print("Complete draft prepared: https://github.com/" + repo + "/releases/tag/" + args.tag)
