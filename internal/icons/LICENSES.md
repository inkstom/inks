# Icon credits

The side bar icons come from **File & Folder Icons** for VS Code by nextbook
(https://github.com/nextbook/vscode-file-icons), MIT License, which uses the
icon set of **file-icons** for Atom (https://github.com/file-icons/atom,
MIT License). `theme.json` is a compact copy of that extension's icon theme.

The fonts in `fonts/` are subsets of the extension's fonts (only the glyphs
the theme uses), converted to TrueType and renamed `inks-<name>`:

| File | Font | Author | License |
| --- | --- | --- | --- |
| `file-icons.ttf` | File-Icons (https://github.com/file-icons/icons) | Daniel Brooker and John Gardner | ISC |
| `fontawesome.ttf` | Font Awesome 4.7 (https://fontawesome.com/v4) | Dave Gandy | SIL Open Font License 1.1, see `OFL.txt` |
| `octicons.ttf` | Octicons (https://github.com/primer/octicons) | GitHub, Inc. | MIT |
| `mfixx.ttf` | Mfizz (https://github.com/file-icons/MFixx) | Mfizz Inc. and file-icons contributors | MIT |
| `devopicons.ttf` | Devicons (https://github.com/file-icons/DevOpicons) | Theodore Vorillas and file-icons contributors | MIT |

`scripts/convert-icons.py` regenerates the fonts and `theme.json` from the
extension's package.
