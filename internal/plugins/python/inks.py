"""The `inks` plugin API module.

Objects are thin handles: every method forwards to the editor over the
plugin host's RPC channel.
"""

import json
import os
import re
import sys

import compat as _compat
import plugin_host as _host

# ---------------------------------------------------------------------------
# Constants

LITERAL = 1
IGNORECASE = 2
WHOLEWORD = 4
REVERSE = 8
WRAP = 16

ENCODED_POSITION = 1
TRANSIENT = 4
FORCE_GROUP = 8
SEMI_TRANSIENT = 16
ADD_TO_SELECTION = 32
REPLACE_MRU = 64
CLEAR_TO_RIGHT = 128

MONOSPACE_FONT = 1
KEEP_OPEN_ON_FOCUS_LOST = 2
WANT_EVENT = 4

HIDE_ON_MOUSE_MOVE = 2
HIDE_ON_MOUSE_MOVE_AWAY = 4
COOPERATE_WITH_AUTO_COMPLETE = 2
HIDE_ON_CHARACTER_EVENT = 8

DRAW_EMPTY = 1
HIDE_ON_MINIMAP = 2
DRAW_EMPTY_AS_OVERWRITE = 4
PERSISTENT = 16
DRAW_OUTLINED = 32
DRAW_NO_FILL = 32
DRAW_NO_OUTLINE = 256
DRAW_SOLID_UNDERLINE = 512
DRAW_STIPPLED_UNDERLINE = 1024
DRAW_SQUIGGLY_UNDERLINE = 2048
HIDDEN = 128

HOVER_TEXT = 1
HOVER_GUTTER = 2
HOVER_MARGIN = 3

INHIBIT_WORD_COMPLETIONS = 8
INHIBIT_EXPLICIT_COMPLETIONS = 16
DYNAMIC_COMPLETIONS = 32
INHIBIT_REORDER = 128

CLASS_WORD_START = 1
CLASS_WORD_END = 2
CLASS_PUNCTUATION_START = 4
CLASS_PUNCTUATION_END = 8
CLASS_SUB_WORD_START = 16
CLASS_SUB_WORD_END = 32
CLASS_LINE_START = 64
CLASS_LINE_END = 128
CLASS_EMPTY_LINE = 256

DIALOG_CANCEL = 0
DIALOG_YES = 1
DIALOG_NO = 2

LAYOUT_INLINE = 0
LAYOUT_BELOW = 1
LAYOUT_BLOCK = 2

KIND_ID_AMBIGUOUS = 0
KIND_ID_KEYWORD = 1
KIND_ID_TYPE = 2
KIND_ID_FUNCTION = 3
KIND_ID_NAMESPACE = 4
KIND_ID_NAVIGATION = 5
KIND_ID_MARKUP = 6
KIND_ID_VARIABLE = 7
KIND_ID_SNIPPET = 8
KIND_AMBIGUOUS = (0, '', '')
KIND_KEYWORD = (1, 'k', 'Keyword')
KIND_TYPE = (2, 't', 'Type')
KIND_FUNCTION = (3, 'f', 'Function')
KIND_NAMESPACE = (4, 'a', 'Namespace')
KIND_NAVIGATION = (5, 'n', 'Navigation')
KIND_MARKUP = (6, 'm', 'Markup')
KIND_VARIABLE = (7, 'v', 'Variable')
KIND_SNIPPET = (8, 's', 'Snippet')

COMPLETION_FORMAT_TEXT = 0
COMPLETION_FORMAT_SNIPPET = 1


def _call(method, **params):
    return _host.call(method, **params)


# ---------------------------------------------------------------------------
# Module functions

def version():
    return _info()['version']


def channel():
    return 'stable'


def platform():
    return {'darwin': 'osx', 'win32': 'windows'}.get(sys.platform, 'linux')


def arch():
    return _info()['arch']


def executable_path():
    return _info()['executable']


def packages_path():
    return _info()['packages']


def installed_packages_path():
    return _info()['installed_packages']


def cache_path():
    return _info()['cache']


_info_cache = None


def _info():
    global _info_cache
    if _info_cache is None:
        _info_cache = _call('app_info')
    return _info_cache


def status_message(msg):
    _call('status_message', text=str(msg))


def error_message(msg):
    _call('message_dialog', text=str(msg), kind='error')


def message_dialog(msg):
    _call('message_dialog', text=str(msg), kind='info')


def ok_cancel_dialog(msg, ok_title='OK', title=''):
    return bool(_call('ok_cancel_dialog', text=str(msg), ok_title=ok_title))


def yes_no_cancel_dialog(msg, yes_title='Yes', no_title='No', title=''):
    return _call('yes_no_cancel_dialog', text=str(msg), yes_title=yes_title, no_title=no_title)


def set_clipboard(text):
    _call('set_clipboard', text=text)


def get_clipboard(size_limit=16777216):
    return _call('get_clipboard') or ''


def set_timeout(fn, delay=0):
    _host.add_timer(fn, delay)


def set_timeout_async(fn, delay=0):
    _host.add_timer(fn, delay)


def run_command(cmd, args=None):
    _call('run_command', name=cmd, args=args or {}, target='app')


def log_commands(flag=None):
    pass


def log_input(flag=None):
    pass


def log_result_regex(flag=None):
    pass


def log_indexing(flag=None):
    pass


def log_build_systems(flag=None):
    pass


def score_selector(scope_name, selector):
    return _score_selector(scope_name, selector)


def active_window():
    wid = _call('active_window')
    return Window(wid) if wid is not None else None


def windows():
    return [Window(w) for w in _call('windows')]


def find_resources(pattern):
    return _call('find_resources', pattern=pattern)


def load_resource(name):
    return _call('load_resource', name=name)


def load_binary_resource(name):
    return load_resource(name).encode('utf-8')


def encode_value(value, pretty=False):
    return json.dumps(value, indent=4 if pretty else None)


def decode_value(data):
    return json.loads(_strip_json_comments(data))


def expand_variables(value, variables):
    def sub(s):
        def rep(m):
            name = m.group(1) or m.group(2)
            return variables.get(name, '')
        return re.sub(r'\$\{(\w+)\}|\$(\w+)', rep, s)
    if isinstance(value, str):
        return sub(value)
    if isinstance(value, list):
        return [expand_variables(v, variables) for v in value]
    if isinstance(value, dict):
        return {k: expand_variables(v, variables) for k, v in value.items()}
    return value


def list_syntaxes():
    return [Syntax(**s) for s in _call('list_syntaxes')]


def find_syntax_by_name(name):
    return [s for s in list_syntaxes() if s.name == name]


def find_syntax_by_scope(scope):
    return [s for s in list_syntaxes() if s.scope == scope]


def find_syntax_for_file(path, first_line=''):
    s = _call('syntax_for_file', path=path, first_line=first_line)
    return Syntax(**s) if s else None


def ui_info():
    return {'theme': {'resolved_value': 'Adaptive.inks-theme'}, 'color_scheme': {'resolved_value': ''}}


def command_url(cmd, args=None):
    return 'subl:%s %s' % (cmd, json.dumps(args or {}))


# ---------------------------------------------------------------------------
# Selectors (a practical subset: alternatives with ',', descendants with ' ',
# exclusion with '-')

def _score_selector(scope_name, selector):
    if not selector or not selector.strip():
        return 1
    scopes = scope_name.split()
    best = 0
    for alt in selector.replace('|', ',').split(','):
        alt = alt.strip()
        if not alt:
            continue
        parts = re.split(r'\s+-\s*|\s*-\s+', alt)
        positive = parts[0].strip()
        if any(_match_path(scopes, p.strip()) for p in parts[1:] if p.strip()):
            continue
        s = _match_path(scopes, positive)
        best = max(best, s)
    return best


def _match_path(scopes, path):
    want = path.split()
    if not want:
        return 1
    i = 0
    score = 0
    for depth, scope in enumerate(scopes):
        if i < len(want) and (scope == want[i] or scope.startswith(want[i] + '.')):
            score += (depth + 1) * 8 + len(want[i].split('.'))
            i += 1
    return score if i == len(want) else 0


def _strip_json_comments(text):
    out = []
    i = 0
    n = len(text)
    while i < n:
        c = text[i]
        if c == '"':
            j = i + 1
            while j < n and text[j] != '"':
                if text[j] == '\\':
                    j += 1
                j += 1
            out.append(text[i:j + 1])
            i = j + 1
        elif text.startswith('//', i):
            while i < n and text[i] != '\n':
                i += 1
        elif text.startswith('/*', i):
            end = text.find('*/', i + 2)
            i = n if end < 0 else end + 2
        else:
            out.append(c)
            i += 1
    return re.sub(r',(\s*[}\]])', r'\1', ''.join(out))


# ---------------------------------------------------------------------------
# Region / Selection

class Region(object):
    __slots__ = ('a', 'b', 'xpos')

    def __init__(self, a, b=None, xpos=-1):
        if b is None:
            b = a
        self.a = a
        self.b = b
        self.xpos = xpos

    def __repr__(self):
        return 'Region(%d, %d)' % (self.a, self.b)

    def __len__(self):
        return self.size()

    def __eq__(self, rhs):
        return isinstance(rhs, Region) and self.a == rhs.a and self.b == rhs.b

    def __hash__(self):
        return hash((self.a, self.b))

    def __lt__(self, rhs):
        lb, rb = self.begin(), rhs.begin()
        return lb < rb or (lb == rb and self.end() < rhs.end())

    def __contains__(self, v):
        if isinstance(v, Region):
            return v.a in self and v.b in self
        return self.begin() <= v <= self.end()

    def __iter__(self):
        return iter((self.a, self.b))

    def to_tuple(self):
        return (self.a, self.b)

    def empty(self):
        return self.a == self.b

    def begin(self):
        return min(self.a, self.b)

    def end(self):
        return max(self.a, self.b)

    def size(self):
        return abs(self.a - self.b)

    def contains(self, x):
        return x in self

    def cover(self, rhs):
        a = min(self.begin(), rhs.begin())
        b = max(self.end(), rhs.end())
        return Region(a, b) if self.a < self.b else Region(b, a)

    def intersection(self, rhs):
        if self.end() <= rhs.begin() or rhs.end() <= self.begin():
            return Region(0, 0)
        return Region(max(self.begin(), rhs.begin()), min(self.end(), rhs.end()))

    def intersects(self, rhs):
        lb, le, rb, re_ = self.begin(), self.end(), rhs.begin(), rhs.end()
        return (lb == rb and le == re_) or (rb > lb and rb < le) or (re_ > lb and re_ < le) or \
            (lb > rb and lb < re_) or (le > rb and le < re_)


def _regions(raw):
    return [Region(a, b) for a, b in raw]


def _raw(regions):
    out = []
    for r in regions:
        if isinstance(r, Region):
            out.append([r.a, r.b])
        elif isinstance(r, int):
            out.append([r, r])
        else:
            out.append([r[0], r[1]])
    return out


class Selection(object):
    def __init__(self, view_id):
        self.view_id = view_id

    def _get(self):
        return _regions(_call('view_sel', view=self.view_id))

    def _set(self, regions):
        _call('view_set_sel', view=self.view_id, regions=_raw(regions))

    def __len__(self):
        return len(self._get())

    def __getitem__(self, index):
        return self._get()[index]

    def __iter__(self):
        return iter(self._get())

    def __delitem__(self, index):
        regions = self._get()
        del regions[index]
        self._set(regions)

    def __eq__(self, rhs):
        return rhs is not None and list(self) == list(rhs)

    def __repr__(self):
        return 'Selection(%r)' % self._get()

    def is_valid(self):
        return True

    def clear(self):
        self._set([])

    def add(self, x):
        regions = self._get()
        regions.append(x if isinstance(x, Region) else Region(x))
        self._set(regions)

    def add_all(self, regions):
        current = self._get()
        current.extend(r if isinstance(r, Region) else Region(*r) if isinstance(r, (list, tuple)) else Region(r) for r in regions)
        self._set(current)

    def subtract(self, region):
        out = []
        for r in self._get():
            if r.intersects(region) or region.contains(r):
                if r.begin() < region.begin():
                    out.append(Region(r.begin(), region.begin()))
                if r.end() > region.end():
                    out.append(Region(region.end(), r.end()))
            else:
                out.append(r)
        self._set(out)

    def contains(self, region):
        return any(region.begin() >= r.begin() and region.end() <= r.end() for r in self._get())


# ---------------------------------------------------------------------------
# Settings

class Settings(object):
    """View/window settings live in the editor; named settings in Python."""

    def __init__(self, target, ident):
        self.target = target
        self.ident = ident

    def get(self, key, default=None):
        if self.target == 'named':
            return _named[self.ident].get(key, default)
        v = _call('settings_get', target=self.target, ident=self.ident, key=key)
        return default if v is None else v

    def has(self, key):
        if self.target == 'named':
            return _named[self.ident].has(key)
        return _call('settings_get', target=self.target, ident=self.ident, key=key) is not None

    def set(self, key, value):
        if self.target == 'named':
            _named[self.ident].set(key, value)
        else:
            _call('settings_set', target=self.target, ident=self.ident, key=key, value=value)

    def erase(self, key):
        if self.target == 'named':
            _named[self.ident].erase(key)
        else:
            _call('settings_set', target=self.target, ident=self.ident, key=key, value=None, erase=True)

    def add_on_change(self, tag, callback):
        if self.target == 'named':
            _named[self.ident].listeners[tag] = callback

    def clear_on_change(self, tag):
        if self.target == 'named':
            _named[self.ident].listeners.pop(tag, None)

    def to_dict(self):
        if self.target == 'named':
            return dict(_named[self.ident].merged())
        return _call('settings_all', target=self.target, ident=self.ident) or {}

    def update(self, other=(), **kwargs):
        for k, v in dict(other, **kwargs).items():
            self.set(k, v)

    def setdefault(self, key, value):
        if not self.has(key):
            self.set(key, value)
        return self.get(key)

    def __getitem__(self, key):
        if not self.has(key):
            raise KeyError(key)
        return self.get(key)

    def __setitem__(self, key, value):
        self.set(key, value)

    def __delitem__(self, key):
        self.erase(key)

    def __contains__(self, key):
        return self.has(key)


class _NamedSettings(object):
    def __init__(self, name):
        self.name = name
        self.base = {}
        self.user = {}
        self.listeners = {}
        self.reload()

    def reload(self):
        self.base = {}
        packages = packages_path()
        try:
            names = sorted(os.listdir(packages))
        except OSError:
            names = []
        for pkg in names:
            if pkg == 'User':
                continue
            for name in reversed(_compat.settings_file_names(self.name)):
                path = os.path.join(packages, pkg, name)
                if os.path.isfile(path):
                    self.base.update(_read_json(path))
        self.user = {}
        for name in reversed(_compat.settings_file_names(self.name)):
            self.user.update(_read_json(os.path.join(packages, 'User', name)))

    def merged(self):
        d = dict(self.base)
        d.update(self.user)
        return d

    def get(self, key, default=None):
        return self.merged().get(key, default)

    def has(self, key):
        return key in self.merged()

    def set(self, key, value):
        self.user[key] = value
        self._changed()

    def erase(self, key):
        self.user.pop(key, None)
        self._changed()

    def _changed(self):
        for cb in list(self.listeners.values()):
            try:
                cb()
            except Exception:
                import traceback
                traceback.print_exc()


def _read_json(path):
    try:
        with open(path, encoding='utf-8') as f:
            data = decode_value(f.read())
        return data if isinstance(data, dict) else {}
    except (OSError, ValueError):
        return {}


_named = {}


def load_settings(base_name):
    if base_name not in _named:
        _named[base_name] = _NamedSettings(base_name)
    return Settings('named', base_name)


def save_settings(base_name):
    s = _named.get(base_name)
    if not s:
        return
    path = os.path.join(packages_path(), 'User', _compat.settings_file_names(base_name)[0])
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, 'w', encoding='utf-8') as f:
        json.dump(s.user, f, indent='\t', sort_keys=True)
        f.write('\n')


def _settings_file_changed(name):
    s = _named.get(name)
    if s:
        s.reload()
        s._changed()


# ---------------------------------------------------------------------------
# Syntax

class Syntax(object):
    def __init__(self, path='', name='', hidden=False, scope=''):
        self.path = path
        self.name = name
        self.hidden = hidden
        self.scope = scope

    def __eq__(self, other):
        return isinstance(other, Syntax) and self.path == other.path

    def __hash__(self):
        return hash(self.path)

    def __repr__(self):
        return 'Syntax(%r, %r, %r, %r)' % (self.path, self.name, self.hidden, self.scope)


# ---------------------------------------------------------------------------
# Edit

class Edit(object):
    def __init__(self, token):
        self.edit_token = token

    def __repr__(self):
        return 'Edit(%d)' % self.edit_token


# ---------------------------------------------------------------------------
# View

class View(object):
    def __init__(self, id):
        self.view_id = id
        self.selection = Selection(id)
        self.settings_object = None

    def __len__(self):
        return self.size()

    def __eq__(self, other):
        return isinstance(other, View) and other.view_id == self.view_id

    def __hash__(self):
        return self.view_id

    def __repr__(self):
        return 'View(%r)' % self.view_id

    def __bool__(self):
        return self.view_id != 0

    def _c(self, method, **params):
        return _call(method, view=self.view_id, **params)

    def id(self):
        return self.view_id

    def buffer_id(self):
        return self._c('view_buffer_id')

    def buffer(self):
        return Buffer(self.buffer_id(), self)

    def is_valid(self):
        return bool(self._c('view_is_valid'))

    def is_primary(self):
        return True

    def window(self):
        wid = self._c('view_window')
        return Window(wid) if wid is not None else None

    def clones(self):
        return [View(v) for v in self._c('view_clones')]

    def file_name(self):
        return self._c('view_file_name')

    def close(self):
        return bool(self._c('view_close'))

    def retarget(self, new_fname):
        self._c('view_retarget', path=new_fname)

    def name(self):
        return self._c('view_name') or ''

    def set_name(self, name):
        self._c('view_set_name', name=name)

    def reset_reference_document(self):
        pass

    def set_reference_document(self, reference):
        pass

    def is_loading(self):
        return False

    def is_dirty(self):
        return bool(self._c('view_is_dirty'))

    def is_read_only(self):
        return bool(self._c('view_is_read_only'))

    def set_read_only(self, read_only):
        self._c('view_set_read_only', value=bool(read_only))

    def is_scratch(self):
        return bool(self._c('view_is_scratch'))

    def set_scratch(self, scratch):
        self._c('view_set_scratch', value=bool(scratch))

    def encoding(self):
        return self._c('view_encoding')

    def set_encoding(self, encoding_name):
        self._c('view_set_encoding', value=encoding_name)

    def line_endings(self):
        return self._c('view_line_endings')

    def set_line_endings(self, line_ending_name):
        self._c('view_set_line_endings', value=line_ending_name)

    def size(self):
        return self._c('view_size')

    def element(self):
        return None

    def substr(self, x):
        if isinstance(x, Region):
            return self._c('view_substr', a=x.begin(), b=x.end())
        return self._c('view_substr', a=x, b=x + 1)

    def insert(self, edit, pt, text):
        return self._c('view_insert', pt=pt, text=text)

    def erase(self, edit, r):
        self._c('view_replace', a=r.begin(), b=r.end(), text='')

    def replace(self, edit, r, text):
        self._c('view_replace', a=r.begin(), b=r.end(), text=text)

    def change_count(self):
        return self._c('view_change_count')

    def change_id(self):
        return (self.change_count(), 0, 0)

    def run_command(self, cmd, args=None):
        _call('run_command', name=cmd, args=args or {}, target='view', view=self.view_id)

    def sel(self):
        return self.selection

    def has_non_empty_selection_region(self):
        return any(not r.empty() for r in self.selection)

    def line(self, x):
        a, b = (x.begin(), x.end()) if isinstance(x, Region) else (x, x)
        return Region(*self._c('view_line', a=a, b=b, full=False))

    def full_line(self, x):
        a, b = (x.begin(), x.end()) if isinstance(x, Region) else (x, x)
        return Region(*self._c('view_line', a=a, b=b, full=True))

    def lines(self, r):
        return _regions(self._c('view_lines', a=r.begin(), b=r.end()))

    def split_by_newlines(self, r):
        return self.lines(r)

    def word(self, x):
        a, b = (x.begin(), x.end()) if isinstance(x, Region) else (x, x)
        return Region(*self._c('view_word', a=a, b=b))

    def classify(self, pt):
        return self._c('view_classify', pt=pt)

    def find_by_class(self, pt, forward, classes, separators=''):
        return self._c('view_find_by_class', pt=pt, forward=forward, classes=classes)

    def expand_by_class(self, x, classes, separators=''):
        a, b = (x.begin(), x.end()) if isinstance(x, Region) else (x, x)
        return Region(*self._c('view_expand_by_class', a=a, b=b, classes=classes))

    def find(self, pattern, start_pt, flags=0):
        r = self._c('view_find', pattern=pattern, start=start_pt, flags=flags)
        return Region(*r) if r else Region(-1, -1)

    def find_all(self, pattern, flags=0, fmt=None, extractions=None):
        res = self._c('view_find_all', pattern=pattern, flags=flags, fmt=fmt)
        if fmt is not None and extractions is not None:
            for r in res:
                extractions.append(r[2])
        return [Region(r[0], r[1]) for r in res]

    def settings(self):
        if self.settings_object is None:
            self.settings_object = Settings('view', self.view_id)
        return self.settings_object

    def meta_info(self, key, pt):
        return None

    def extract_tokens_with_scopes(self, r):
        return []

    def extract_scope(self, pt):
        return Region(*self._c('view_extract_scope', pt=pt))

    def expand_to_scope(self, pt, selector):
        r = self._c('view_extract_scope', pt=pt)
        return Region(*r) if r else None

    def scope_name(self, pt):
        return self._c('view_scope_name', pt=pt)

    def context_backtrace(self, pt):
        return self.scope_name(pt).split()

    def match_selector(self, pt, selector):
        return _score_selector(self.scope_name(pt), selector) > 0

    def score_selector(self, pt, selector):
        return _score_selector(self.scope_name(pt), selector)

    def find_by_selector(self, selector):
        return _regions(self._c('view_find_by_selector', selector=selector))

    def style(self):
        return self._c('view_style')

    def style_for_scope(self, scope):
        return self._c('view_style_for_scope', scope=scope)

    def indented_region(self, pt):
        return Region(*self._c('view_indented_region', pt=pt))

    def indentation_level(self, pt):
        return self._c('view_indentation_level', pt=pt)

    def has_inverted_selection(self):
        return False

    def is_in_edit(self):
        return False

    def rowcol(self, tp):
        return tuple(self._c('view_rowcol', pt=tp))

    def rowcol_utf8(self, tp):
        return self.rowcol(tp)

    def rowcol_utf16(self, tp):
        return self.rowcol(tp)

    def text_point(self, row, col, *, clamp_column=False):
        return self._c('view_text_point', row=row, col=col)

    def text_point_utf8(self, row, col, *, clamp_column=False):
        return self.text_point(row, col)

    def text_point_utf16(self, row, col, *, clamp_column=False):
        return self.text_point(row, col)

    def visible_region(self):
        return Region(*self._c('view_visible_region'))

    def show(self, x, show_surrounds=True, keep_to_left=False, animate=True):
        a, b = (x.begin(), x.end()) if isinstance(x, Region) else (x, x) if isinstance(x, int) else (x[0].begin(), x[0].end()) if len(x) else (0, 0)
        self._c('view_show', a=a, b=b, center=False)

    def show_at_center(self, x, animate=True):
        a, b = (x.begin(), x.end()) if isinstance(x, Region) else (x, x)
        self._c('view_show', a=a, b=b, center=True)

    def viewport_position(self):
        return tuple(self._c('view_viewport_position'))

    def set_viewport_position(self, xy, animate=True):
        self._c('view_set_viewport_position', x=xy[0], y=xy[1])

    def viewport_extent(self):
        return tuple(self._c('view_viewport_extent'))

    def layout_extent(self):
        return self.viewport_extent()

    def text_to_layout(self, tp):
        return tuple(self._c('view_text_to_layout', pt=tp))

    def text_to_window(self, tp):
        return self.text_to_layout(tp)

    def layout_to_text(self, xy):
        return self._c('view_layout_to_text', x=xy[0], y=xy[1])

    def window_to_text(self, xy):
        return self.layout_to_text(xy)

    def line_height(self):
        return self._c('view_line_height')

    def em_width(self):
        return self._c('view_em_width')

    def is_folded(self, sr):
        return False

    def folded_regions(self):
        return []

    def fold(self, x):
        regions = [x] if isinstance(x, Region) else list(x)
        return bool(self._c('view_fold', regions=_raw(regions)))

    def unfold(self, x):
        regions = [x] if isinstance(x, Region) else list(x)
        self._c('view_unfold', regions=_raw(regions))
        return regions

    def add_regions(self, key, regions, scope='', icon='', flags=0, annotations=None, annotation_color='', on_navigate=None, on_close=None):
        self._c('view_add_regions', key=key, regions=_raw(regions), scope=scope, icon=icon, flags=flags,
                annotations=annotations or [])

    def get_regions(self, key):
        return _regions(self._c('view_get_regions', key=key))

    def erase_regions(self, key):
        self._c('view_erase_regions', key=key)

    def add_phantom(self, key, region, content, layout, on_navigate=None):
        return self._c('view_add_phantom', key=key, a=region.begin(), b=region.end(), content=content, layout=layout)

    def erase_phantoms(self, key):
        self._c('view_erase_phantoms', key=key)

    def erase_phantom_by_id(self, pid):
        pass

    def query_phantom(self, pid):
        return []

    def query_phantoms(self, pids):
        return []

    def assign_syntax(self, syntax):
        if isinstance(syntax, Syntax):
            syntax = syntax.path
        self._c('view_set_syntax', syntax=syntax)

    def set_syntax_file(self, syntax_file):
        self.assign_syntax(syntax_file)

    def syntax(self):
        s = self._c('view_syntax')
        return Syntax(**s) if s else None

    def symbols(self):
        return [(Region(a, b), name) for a, b, name in self._c('view_symbols')]

    def get_symbols(self):
        return self.symbols()

    def indexed_symbols(self):
        return self.symbols()

    def indexed_references(self):
        return []

    def symbol_regions(self):
        return [SymbolRegion(name, Region(a, b), '', (0, '', ''), 1) for a, b, name in self._c('view_symbols')]

    def indexed_symbol_regions(self, type=3):
        return self.symbol_regions()

    def set_status(self, key, value):
        self._c('view_set_status', key=key, value=str(value))

    def get_status(self, key):
        return self._c('view_get_status', key=key) or ''

    def erase_status(self, key):
        self._c('view_set_status', key=key, value=None)

    def extract_completions(self, prefix, tp=-1):
        return self._c('view_extract_completions', prefix=prefix)

    def command_history(self, index, modifying_only=False):
        return (None, None, 0)

    def overwrite_status(self):
        return False

    def set_overwrite_status(self, value):
        pass

    def show_popup_menu(self, items, on_select, flags=0):
        cid = _register_callback(on_select)
        self._c('view_show_popup_menu', items=items, callback=cid)

    def show_popup(self, content, flags=0, location=-1, max_width=320, max_height=240, on_navigate=None, on_hide=None):
        nav = _register_callback(on_navigate) if on_navigate else None
        self._c('view_show_popup', content=content, location=location, max_width=max_width, max_height=max_height, on_navigate=nav)

    def update_popup(self, content):
        self._c('view_update_popup', content=content)

    def is_popup_visible(self):
        return bool(self._c('view_is_popup_visible'))

    def hide_popup(self):
        self._c('view_hide_popup')

    def is_auto_complete_visible(self):
        return bool(self._c('view_is_auto_complete_visible'))

    def preserve_auto_complete_on_focus_lost(self):
        pass

    def export_to_html(self, regions=None, minihtml=False, enclosing_tags=False, font_size=True, font_family=True):
        return '<pre>%s</pre>' % self.substr(Region(0, self.size()))

    def clear_undo_stack(self):
        pass


class SymbolRegion(object):
    def __init__(self, name, region, syntax, type, kind):
        self.name = name
        self.region = region
        self.syntax = syntax
        self.type = type
        self.kind = kind


class Buffer(object):
    def __init__(self, id, view=None):
        self.buffer_id = id
        self._view = view

    def id(self):
        return self.buffer_id

    def file_name(self):
        return self._view.file_name() if self._view else None

    def views(self):
        return [self._view] if self._view else []

    def primary_view(self):
        return self._view


# ---------------------------------------------------------------------------
# Window

class Window(object):
    def __init__(self, id):
        self.window_id = id
        self.settings_object = None
        self.template_settings_object = None

    def __eq__(self, other):
        return isinstance(other, Window) and other.window_id == self.window_id

    def __hash__(self):
        return hash(self.window_id)

    def __bool__(self):
        return self.window_id is not None

    def __repr__(self):
        return 'Window(%r)' % self.window_id

    def _c(self, method, **params):
        return _call(method, window=self.window_id, **params)

    def id(self):
        return self.window_id

    def is_valid(self):
        return True

    def hwnd(self):
        return 0

    def active_sheet(self):
        v = self.active_view()
        return Sheet(v) if v else None

    def active_view(self):
        vid = self._c('window_active_view')
        return View(vid) if vid else None

    def new_html_sheet(self, name, contents, flags=0, group=-1):
        v = self.new_file()
        v.set_name(name)
        v.run_command('append', {'characters': re.sub(r'<[^>]+>', '', contents)})
        v.set_scratch(True)
        return Sheet(v)

    def run_command(self, cmd, args=None):
        _call('run_command', name=cmd, args=args or {}, target='window', window=self.window_id)

    def new_file(self, flags=0, syntax=''):
        return View(self._c('window_new_file', syntax=syntax))

    def open_file(self, fname, flags=0, group=-1):
        return View(self._c('window_open_file', path=fname, encoded=bool(flags & ENCODED_POSITION),
                            transient=bool(flags & TRANSIENT), group=group))

    def find_open_file(self, fname, group=-1):
        vid = self._c('window_find_open_file', path=fname)
        return View(vid) if vid else None

    def file_history(self):
        return []

    def num_groups(self):
        return self._c('window_num_groups')

    def active_group(self):
        return self._c('window_active_group')

    def focus_group(self, idx):
        self._c('window_focus_group', group=idx)

    def focus_sheet(self, sheet):
        if sheet and sheet.view():
            self.focus_view(sheet.view())

    def focus_view(self, view):
        self._c('window_focus_view', target=view.view_id)

    def select_sheets(self, sheets):
        pass

    def bring_to_front(self):
        pass

    def get_sheet_index(self, sheet):
        return self.get_view_index(sheet.view())

    def get_view_index(self, view):
        return tuple(self._c('window_get_view_index', target=view.view_id))

    def set_sheet_index(self, sheet, group, idx):
        self.set_view_index(sheet.view(), group, idx)

    def set_view_index(self, view, group, idx):
        self._c('window_set_view_index', target=view.view_id, group=group, index=idx)

    def sheets(self):
        return [Sheet(v) for v in self.views()]

    def views(self, *, include_transient=False):
        return [View(v) for v in self._c('window_views', group=-1)]

    def selected_sheets(self):
        v = self.active_view()
        return [Sheet(v)] if v else []

    def active_sheet_in_group(self, group):
        v = self.active_view_in_group(group)
        return Sheet(v) if v else None

    def active_view_in_group(self, group):
        vid = self._c('window_active_view_in_group', group=group)
        return View(vid) if vid else None

    def sheets_in_group(self, group):
        return [Sheet(v) for v in self.views_in_group(group)]

    def views_in_group(self, group):
        return [View(v) for v in self._c('window_views', group=group)]

    def transient_sheet_in_group(self, group):
        return None

    def transient_view_in_group(self, group):
        return None

    def layout(self):
        return self._c('window_layout')

    def get_layout(self):
        return self.layout()

    def set_layout(self, layout):
        self._c('window_set_layout', layout=layout)

    def create_output_panel(self, name, unlisted=False):
        return View(self._c('window_create_output_panel', name=name))

    def find_output_panel(self, name):
        vid = self._c('window_find_output_panel', name=name)
        return View(vid) if vid else None

    def destroy_output_panel(self, name):
        self._c('window_destroy_output_panel', name=name)

    def active_panel(self):
        return self._c('window_active_panel')

    def panels(self):
        return self._c('window_panels')

    def get_output_panel(self, name):
        return self.find_output_panel(name)

    def show_input_panel(self, caption, initial_text, on_done, on_change, on_cancel):
        self._c('window_show_input_panel', caption=caption, initial=initial_text,
                on_done=_register_callback(on_done), on_change=_register_callback(on_change),
                on_cancel=_register_callback(on_cancel))
        return None

    def show_quick_panel(self, items, on_select, flags=0, selected_index=-1, on_highlight=None, placeholder=None):
        norm = []
        for it in items:
            if isinstance(it, str):
                norm.append([it])
            elif isinstance(it, QuickPanelItem):
                norm.append([it.trigger, it.details if isinstance(it.details, str) else ' '.join(it.details or []), it.annotation])
            else:
                norm.append([str(x) for x in it])
        self._c('window_show_quick_panel', items=norm, on_select=_register_callback(on_select),
                on_highlight=_register_callback(on_highlight), selected_index=selected_index,
                placeholder=placeholder or '')

    def is_sidebar_visible(self):
        return bool(self._c('window_ui_visible', what='sidebar'))

    def set_sidebar_visible(self, flag, animate=True):
        self._c('window_set_ui_visible', what='sidebar', value=flag)

    def is_minimap_visible(self):
        return bool(self._c('window_ui_visible', what='minimap'))

    def set_minimap_visible(self, flag):
        self._c('window_set_ui_visible', what='minimap', value=flag)

    def is_status_bar_visible(self):
        return bool(self._c('window_ui_visible', what='status_bar'))

    def set_status_bar_visible(self, flag):
        self._c('window_set_ui_visible', what='status_bar', value=flag)

    def get_tabs_visible(self):
        return bool(self._c('window_ui_visible', what='tabs'))

    def set_tabs_visible(self, flag):
        self._c('window_set_ui_visible', what='tabs', value=flag)

    def is_menu_visible(self):
        return True

    def set_menu_visible(self, flag):
        pass

    def folders(self):
        return self._c('window_folders')

    def project_file_name(self):
        return self._c('window_project_file_name')

    def workspace_file_name(self):
        return None

    def project_data(self):
        return self._c('window_project_data')

    def set_project_data(self, v):
        self._c('window_set_project_data', data=v)

    def settings(self):
        if self.settings_object is None:
            self.settings_object = Settings('window', self.window_id)
        return self.settings_object

    def template_settings(self):
        return self.settings()

    def symbol_locations(self, sym, source=1, type=3, kind_id=0, kind_letter=''):
        return [SymbolLocation(**s) for s in self._c('window_lookup_symbol', symbol=sym)]

    def lookup_symbol_in_index(self, sym):
        return [(s.path, s.display_name, s.row_col) for s in self.symbol_locations(sym)]

    def lookup_symbol_in_open_files(self, sym):
        return self.lookup_symbol_in_index(sym)

    def lookup_references_in_index(self, sym):
        return []

    def lookup_references_in_open_files(self, sym):
        return []

    def extract_variables(self):
        return self._c('window_extract_variables')

    def status_message(self, msg):
        status_message(msg)

    def promote_sheet(self, sheet):
        pass


class SymbolLocation(object):
    def __init__(self, path, display_name, row, col, syntax='', type=1, kind=(0, '', '')):
        self.path = path
        self.display_name = display_name
        self.row = row
        self.col = col
        self.row_col = (row, col)
        self.syntax = syntax
        self.type = type
        self.kind = kind

    def path_encoded_position(self):
        return '%s:%d:%d' % (self.path, self.row, self.col)


class Sheet(object):
    def __init__(self, view):
        self._view = view

    def id(self):
        return self._view.view_id

    def view(self):
        return self._view

    def window(self):
        return self._view.window()

    def file_name(self):
        return self._view.file_name()

    def group(self):
        return self.window().get_view_index(self._view)[0]

    def is_semi_transient(self):
        return False

    def is_transient(self):
        return False


class QuickPanelItem(object):
    def __init__(self, trigger, details='', annotation='', kind=KIND_AMBIGUOUS):
        self.trigger = trigger
        self.details = details
        self.annotation = annotation
        self.kind = kind


class ListInputItem(object):
    def __init__(self, text, value, details='', annotation='', kind=KIND_AMBIGUOUS):
        self.text = text
        self.value = value
        self.details = details
        self.annotation = annotation
        self.kind = kind


class CompletionItem(object):
    def __init__(self, trigger, annotation='', completion='', completion_format=COMPLETION_FORMAT_TEXT,
                 kind=KIND_AMBIGUOUS, details=''):
        self.trigger = trigger
        self.annotation = annotation
        self.completion = completion or trigger
        self.completion_format = completion_format
        self.kind = kind
        self.details = details

    @classmethod
    def snippet_completion(cls, trigger, snippet, annotation='', kind=KIND_SNIPPET, details=''):
        return cls(trigger, annotation, snippet, COMPLETION_FORMAT_SNIPPET, kind, details)

    @classmethod
    def command_completion(cls, trigger, command, args=None, annotation='', kind=KIND_AMBIGUOUS, details=''):
        return cls(trigger, annotation, trigger, COMPLETION_FORMAT_TEXT, kind, details)


class CompletionList(object):
    def __init__(self, completions=None, flags=0):
        self.completions = completions
        self.flags = flags
        self.target = None

    def set_completions(self, completions, flags=0):
        self.completions = completions
        self.flags = flags


class HistoricPosition(object):
    pass


class Html(object):
    def __init__(self, text, base_dir=None):
        self.data = text
        self.base_dir = base_dir


class Phantom(object):
    def __init__(self, region, content, layout, on_navigate=None):
        self.region = region
        self.content = content
        self.layout = layout
        self.on_navigate = on_navigate
        self.id = None


class PhantomSet(object):
    def __init__(self, view, key=''):
        self.view = view
        self.key = key
        self.phantoms = []

    def update(self, new_phantoms):
        self.view.erase_phantoms(self.key)
        for p in new_phantoms:
            p.id = self.view.add_phantom(self.key, p.region, p.content, p.layout, p.on_navigate)
        self.phantoms = list(new_phantoms)


# ---------------------------------------------------------------------------
# Callbacks registered with the editor (quick panels, input panels, popups)

_callbacks = {}
_next_callback = [0]


def _register_callback(fn):
    if fn is None:
        return None
    _next_callback[0] += 1
    cid = _next_callback[0]
    _callbacks[cid] = fn
    return cid


@_host.handler('callback')
def _invoke_callback(id, args=None, release=False):
    fn = _callbacks.get(id)
    if release:
        _callbacks.pop(id, None)
    if fn:
        fn(*(args or []))
    return True
