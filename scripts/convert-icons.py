#!/usr/bin/env python3
# Regenerate internal/icons from the File & Folder Icons VS Code extension.
#
#   python3 -m venv /tmp/fontenv && /tmp/fontenv/bin/pip install fonttools brotli
#   curl -L -o ext.vsix.gz "https://marketplace.visualstudio.com/_apis/public/gallery/publishers/nextbook/vsextensions/file-and-folder-icons/latest/vspackage"
#   gunzip -c ext.vsix.gz > ext.vsix && unzip ext.vsix -d ext
#   /tmp/fontenv/bin/python scripts/convert-icons.py ext/extension/icons internal/icons

"""Convert the File & Folder Icons theme (nextbook, MIT) for inks:
WOFF2 fonts -> TTF subset to used glyphs; theme JSON -> compact JSON."""
import json, sys, os
from fontTools.ttLib import TTFont
from fontTools import subset

src, out = sys.argv[1], sys.argv[2]
theme = json.load(open(os.path.join(src, 'file-icons-icon-theme.json')))
fonts = [f['id'] for f in theme['fonts']]
defs = theme['iconDefinitions']

def cp(ch):  # "\\f016" -> 0xf016
    s = ch.replace('\\', '')
    return int(s, 16) if len(s) > 1 else ord(s)

used = {f: set() for f in fonts}
compact = {}
for k, d in defs.items():
    c = cp(d['fontCharacter'])
    used[d['fontId']].add(c)
    compact[k] = [fonts.index(d['fontId']), c, d.get('fontColor', '')]

for f in theme['fonts']:
    fid = f['id']
    tt = TTFont(os.path.join(src, f['src'][0]['path'].lstrip('./')))
    tt.flavor = None
    opts = subset.Options()
    opts.notdef_outline = True
    opts.layout_features = []
    opts.name_IDs = ['*']
    sub = subset.Subsetter(opts)
    sub.populate(unicodes=sorted(used[fid]))
    sub.subset(tt)
    # A modified (subset) font gets its own name, as the fonts' licenses ask.
    newname = 'inks-' + fid
    for rec in tt['name'].names:
        if rec.nameID in (1, 3, 4, 6, 16, 17):
            rec.string = newname if rec.nameID != 3 else newname + '-subset'
    path = os.path.join(out, 'fonts', fid + '.ttf')
    tt.save(path)
    print(f'{fid}: {len(used[fid])} glyphs, {os.path.getsize(path)} bytes')

def section(t):
    return {k: t[k] for k in ['file', 'folder', 'folderExpanded', 'fileExtensions', 'fileNames', 'folderNames', 'folderNamesExpanded'] if k in t}

data = {'fonts': fonts, 'defs': compact, 'dark': section(theme), 'light': section(theme['light']),
        'rootFolder': theme.get('rootFolder'), 'rootFolderExpanded': theme.get('rootFolderExpanded')}
with open(os.path.join(out, 'theme.json'), 'w') as fh:
    json.dump(data, fh, separators=(',', ':'), sort_keys=True)
print('theme.json', os.path.getsize(os.path.join(out, 'theme.json')), 'bytes')
