"""Compatibility with packages written for an existing plugin ecosystem.

Those packages import modules named `sublime` / `sublime_plugin` and ship
resources named `*.sublime-*`. inks accepts them as aliases of its own
`inks` / `inks_plugin` modules and `*.inks-*` resources. Nothing here is
shown in the UI.
"""

import sys

LEGACY_MODULES = {'sublime': 'inks', 'sublime_plugin': 'inks_plugin'}
LEGACY_RESOURCE_PREFIX = '.sublime-'
RESOURCE_PREFIX = '.inks-'
PACKAGE_ARCHIVE_EXTS = ('.inks-package', '.sublime-package')


def install_module_aliases():
    for legacy, own in LEGACY_MODULES.items():
        if own in sys.modules:
            sys.modules.setdefault(legacy, sys.modules[own])


def settings_file_names(name):
    """Both spellings of a settings file name, preferred one first."""
    if name.endswith(LEGACY_RESOURCE_PREFIX + 'settings'):
        own = name[:-len(LEGACY_RESOURCE_PREFIX + 'settings')] + RESOURCE_PREFIX + 'settings'
        return [own, name]
    if name.endswith(RESOURCE_PREFIX + 'settings'):
        return [name, name[:-len(RESOURCE_PREFIX + 'settings')] + LEGACY_RESOURCE_PREFIX + 'settings']
    return [name]


def archive_package_name(filename):
    for ext in PACKAGE_ARCHIVE_EXTS:
        if filename.endswith(ext):
            return filename[:-len(ext)]
    return None
