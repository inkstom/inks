"""The `inks_plugin` module: command and event listener base classes,
plugin discovery and dispatch."""

import code
import importlib
import importlib.util
import inspect
import os
import re
import sys
import traceback
import types
import zipfile

import compat
import plugin_host as _host
import inks

# ---------------------------------------------------------------------------
# Base classes


class Command(object):
    def name(self):
        return _command_name(type(self).__name__)

    def is_enabled(self, *args, **kwargs):
        return True

    def is_visible(self, *args, **kwargs):
        return True

    def is_checked(self, *args, **kwargs):
        return False

    def description(self, *args, **kwargs):
        return None

    def want_event(self):
        return False

    def input(self, args):
        return None

    def input_description(self):
        return None


class ApplicationCommand(Command):
    def run(self, **kwargs):
        pass


class WindowCommand(Command):
    def __init__(self, window):
        self.window = window

    def run(self, **kwargs):
        pass


class TextCommand(Command):
    def __init__(self, view):
        self.view = view

    def run(self, edit, **kwargs):
        pass


class EventListener(object):
    pass


class ViewEventListener(object):
    @classmethod
    def is_applicable(cls, settings):
        return True

    @classmethod
    def applies_to_primary_view_only(cls):
        return True

    def __init__(self, view):
        self.view = view


class TextChangeListener(object):
    def __init__(self):
        self.buffer = None

    @classmethod
    def is_applicable(cls, buffer):
        return True

    def attach(self, buffer):
        self.buffer = buffer

    def detach(self):
        self.buffer = None

    def is_attached(self):
        return self.buffer is not None


class CommandInputHandler(object):
    def name(self):
        return _command_name(type(self).__name__.replace('InputHandler', ''))

    def placeholder(self):
        return ''

    def initial_text(self):
        return ''

    def preview(self, arg):
        return ''

    def validate(self, arg):
        return True

    def cancel(self):
        pass

    def confirm(self, arg):
        pass

    def next_input(self, args):
        return None

    def want_event(self):
        return False


class TextInputHandler(CommandInputHandler):
    def description(self, text):
        return text


class ListInputHandler(CommandInputHandler):
    def list_items(self):
        return []

    def description(self, v, text):
        return text


class BackInputHandler(CommandInputHandler):
    pass


# ---------------------------------------------------------------------------
# Registry

all_command_classes = [[], [], []]  # application, window, text
all_callbacks = {}
view_event_listener_classes = []
_view_listeners = {}  # view id -> [instances]
_event_listeners = []
_plugins = {}  # module name -> module

EVENTS = [
    'on_new', 'on_clone', 'on_load', 'on_pre_close', 'on_close', 'on_pre_save', 'on_post_save',
    'on_modified', 'on_selection_modified', 'on_activated', 'on_deactivated', 'on_query_context',
    'on_query_completions', 'on_hover', 'on_text_command', 'on_window_command', 'on_post_text_command',
    'on_post_window_command', 'on_new_window', 'on_pre_close_window', 'on_exit', 'on_reload',
    'on_load_project', 'on_pre_save_project', 'on_post_save_project', 'on_pre_close_project',
    'on_new_buffer', 'on_close_buffer', 'on_init', 'on_post_move', 'on_pre_move', 'on_revert',
]


def _command_name(clsname):
    name = clsname[:-7] if clsname.endswith('Command') else clsname
    name = re.sub(r'([A-Z]+)([A-Z][a-z])', r'\1_\2', name)
    name = re.sub(r'([a-z\d])([A-Z])', r'\1_\2', name)
    return name.lower()


def _kind(cls):
    if issubclass(cls, TextCommand):
        return 'text'
    if issubclass(cls, WindowCommand):
        return 'window'
    return 'app'


def _register_module(module):
    for name, obj in list(vars(module).items()):
        if not inspect.isclass(obj) or obj.__module__ != module.__name__:
            continue
        if issubclass(obj, (ApplicationCommand, WindowCommand, TextCommand)) and obj not in (ApplicationCommand, WindowCommand, TextCommand):
            idx = {'app': 0, 'window': 1, 'text': 2}[_kind(obj)]
            all_command_classes[idx].append(obj)
        elif issubclass(obj, ViewEventListener) and obj is not ViewEventListener:
            view_event_listener_classes.append(obj)
        elif issubclass(obj, EventListener) and obj is not EventListener:
            try:
                _event_listeners.append(obj())
            except Exception:
                traceback.print_exc()


def _unregister_module(modname):
    for lst in all_command_classes:
        lst[:] = [c for c in lst if c.__module__ != modname]
    view_event_listener_classes[:] = [c for c in view_event_listener_classes if c.__module__ != modname]
    _event_listeners[:] = [l for l in _event_listeners if type(l).__module__ != modname]
    for vid in list(_view_listeners):
        _view_listeners[vid] = [l for l in _view_listeners[vid] if type(l).__module__ != modname]


def _find_command(name):
    for kind, idx in (('text', 2), ('window', 1), ('app', 0)):
        for cls in reversed(all_command_classes[idx]):
            if _command_name(cls.__name__) == name:
                return kind, cls
    return None, None


# ---------------------------------------------------------------------------
# Plugin loading

def _ensure_package(pkg, path):
    if pkg in sys.modules:
        return
    m = types.ModuleType(pkg)
    m.__path__ = [path]
    m.__package__ = pkg
    sys.modules[pkg] = m


def _load_file(pkg, path):
    mod = os.path.splitext(os.path.basename(path))[0]
    modname = '%s.%s' % (pkg, mod)
    if modname in _plugins:
        _unload(modname)
    spec = importlib.util.spec_from_file_location(modname, path)
    module = importlib.util.module_from_spec(spec)
    module.__package__ = pkg
    sys.modules[modname] = module
    try:
        spec.loader.exec_module(module)
    except Exception:
        sys.modules.pop(modname, None)
        print('Error loading plugin %s:' % modname)
        traceback.print_exc()
        return None
    _plugins[modname] = module
    _register_module(module)
    if hasattr(module, 'plugin_loaded'):
        try:
            module.plugin_loaded()
        except Exception:
            traceback.print_exc()
    print('loaded plugin %s' % modname)
    return modname


def _unload(modname):
    module = _plugins.pop(modname, None)
    if module and hasattr(module, 'plugin_unloaded'):
        try:
            module.plugin_unloaded()
        except Exception:
            traceback.print_exc()
    _unregister_module(modname)
    sys.modules.pop(modname, None)


def _extract_installed(installed, cache):
    """Unzip package archives so they load like folders."""
    out = []
    if not os.path.isdir(installed):
        return out
    for name in sorted(os.listdir(installed)):
        pkg = compat.archive_package_name(name)
        if not pkg:
            continue
        dest = os.path.join(cache, 'Installed', pkg)
        src = os.path.join(installed, name)
        stamp = os.path.join(dest, '.stamp')
        mtime = str(os.path.getmtime(src))
        if not (os.path.isfile(stamp) and open(stamp).read() == mtime):
            try:
                with zipfile.ZipFile(src) as z:
                    z.extractall(dest)
                with open(stamp, 'w') as f:
                    f.write(mtime)
            except Exception:
                traceback.print_exc()
                continue
        out.append((pkg, dest))
    return out


def _package_dirs():
    packages = inks.packages_path()
    dirs = []
    seen = set()
    if os.path.isdir(packages):
        for name in sorted(os.listdir(packages), key=lambda n: (n == 'User', n.lower())):
            p = os.path.join(packages, name)
            if os.path.isdir(p) and not name.startswith('.'):
                dirs.append((name, p))
                seen.add(name)
    for pkg, path in _extract_installed(inks.installed_packages_path(), inks.cache_path()):
        if pkg not in seen:
            dirs.insert(0, (pkg, path))
    return dirs


def _command_list():
    out = []
    for kind, idx in (('app', 0), ('window', 1), ('text', 2)):
        for cls in all_command_classes[idx]:
            out.append({'name': _command_name(cls.__name__), 'kind': kind, 'module': cls.__module__})
    return out


@_host.handler('load_plugins')
def load_plugins():
    packages = inks.packages_path()
    if packages not in sys.path:
        sys.path.insert(0, packages)
    loaded = []
    # Legacy dependencies ship their modules in all/, st3/ or st4/.
    for pkg, path in _package_dirs():
        for sub in ('all', 'st4', 'st3', 'st3_osx_x64', 'st4_py38'):
            p = os.path.join(path, sub)
            if os.path.isdir(p) and p not in sys.path and not os.path.isfile(os.path.join(path, sub + '.py')):
                sys.path.insert(0, p)
    lib = os.path.join(os.path.dirname(inks.packages_path()), 'Lib', 'python3')
    if os.path.isdir(lib) and lib not in sys.path:
        sys.path.insert(0, lib)
    for pkg, path in _package_dirs():
        _ensure_package(pkg, path)
        for name in sorted(os.listdir(path)):
            if name.endswith('.py') and not name.startswith('.'):
                m = _load_file(pkg, os.path.join(path, name))
                if m:
                    loaded.append(m)
    _call_listeners('on_init', [])
    return {'plugins': loaded, 'commands': _command_list(), 'events': _event_names(),
            'packages': [p for p, _ in _package_dirs()]}


@_host.handler('reload_plugin')
def reload_plugin(path):
    pkg = os.path.basename(os.path.dirname(path))
    _ensure_package(pkg, os.path.dirname(path))
    if os.path.isfile(path):
        _load_file(pkg, path)
    else:
        _unload('%s.%s' % (pkg, os.path.splitext(os.path.basename(path))[0]))
    return {'commands': _command_list(), 'events': _event_names()}


@_host.handler('settings_changed')
def settings_changed(name):
    inks._settings_file_changed(name)
    return True


def _event_names():
    names = set()
    for l in _event_listeners:
        for e in EVENTS:
            if hasattr(l, e) or hasattr(l, e + '_async'):
                names.add(e)
    for cls in view_event_listener_classes:
        for e in EVENTS:
            if hasattr(cls, e) or hasattr(cls, e + '_async'):
                names.add(e)
    return sorted(names)


# ---------------------------------------------------------------------------
# Commands

class _EditContext(object):
    token = 0


@_host.handler('run_command')
def run_command(name, args=None, view=None, window=None, query=None):
    """Run (or query) a plugin command. Returns False if not found."""
    kind, cls = _find_command(name)
    if not cls:
        return {'found': False}
    args = args or {}
    if kind == 'text':
        if not view:
            return {'found': True, 'ran': False}
        inst = cls(inks.View(view))
    elif kind == 'window':
        inst = cls(inks.Window(window))
    else:
        inst = cls()
    if query:
        return {
            'found': True,
            'enabled': bool(_safe(lambda: inst.is_enabled(**args), True)),
            'visible': bool(_safe(lambda: inst.is_visible(**args), True)),
            'checked': bool(_safe(lambda: inst.is_checked(**args), False)),
            'description': _safe(lambda: inst.description(**args), None),
        }
    if not _safe(lambda: inst.is_enabled(**args), True):
        return {'found': True, 'ran': False}
    try:
        if kind == 'text':
            _EditContext.token += 1
            inst.run(inks.Edit(_EditContext.token), **args)
        else:
            inst.run(**args)
    except TypeError as e:
        # A command with required arguments run without them: offer input handlers.
        handler = _safe(lambda: inst.input(args), None)
        if handler is not None:
            _run_input(inst, kind, args, handler)
            return {'found': True, 'ran': True}
        traceback.print_exc()
        return {'found': True, 'ran': False, 'error': str(e)}
    except Exception as e:
        traceback.print_exc()
        return {'found': True, 'ran': False, 'error': str(e)}
    return {'found': True, 'ran': True}


def _run_input(inst, kind, args, handler):
    """Minimal input handler support via quick/input panels."""
    win = inst.window if kind == 'window' else (inst.view.window() if kind == 'text' else inks.active_window())
    arg_name = handler.name()

    def finish(value):
        new_args = dict(args)
        new_args[arg_name] = value
        nxt = _safe(lambda: handler.next_input(new_args), None)
        if nxt is not None:
            _run_input(inst, kind, new_args, nxt)
            return
        if kind == 'text':
            inst.view.run_command(inst.name(), new_args)
        else:
            win.run_command(inst.name(), new_args)

    if isinstance(handler, ListInputHandler):
        items = handler.list_items()
        if isinstance(items, tuple):
            items = items[0]
        captions = []
        values = []
        for it in items:
            if isinstance(it, inks.ListInputItem):
                captions.append(it.text)
                values.append(it.value)
            elif isinstance(it, (list, tuple)):
                captions.append(str(it[0]))
                values.append(it[1])
            else:
                captions.append(str(it))
                values.append(it)
        win.show_quick_panel(captions, lambda i: i >= 0 and finish(values[i]), placeholder=handler.placeholder())
    else:
        win.show_input_panel(handler.placeholder() or arg_name, handler.initial_text(), finish, None, None)


def _safe(fn, default):
    try:
        return fn()
    except Exception:
        return default


# ---------------------------------------------------------------------------
# Events

def _listeners_for_view(view_id):
    if view_id not in _view_listeners:
        view = inks.View(view_id)
        lst = []
        for cls in view_event_listener_classes:
            try:
                if cls.is_applicable(view.settings()):
                    lst.append(cls(view))
            except Exception:
                traceback.print_exc()
        _view_listeners[view_id] = lst
    return _view_listeners[view_id]


def _call_listeners(event, args, view_id=None, collect=False):
    results = []
    targets = list(_event_listeners)
    if view_id is not None and view_event_listener_classes:
        targets += _listeners_for_view(view_id)
    for l in targets:
        is_view = isinstance(l, ViewEventListener)
        for name in (event, event + '_async'):
            fn = getattr(l, name, None)
            if not fn:
                continue
            try:
                call_args = args[1:] if (is_view and args and isinstance(args[0], inks.View)) else args
                r = fn(*call_args)
                if collect and r is not None:
                    results.append(r)
            except Exception:
                traceback.print_exc()
    return results


@_host.handler('event')
def event(name, view=None, window=None, args=None):
    args = args or []
    v = inks.View(view) if view else None
    w = inks.Window(window) if window else None
    if name in ('on_new_window', 'on_pre_close_window', 'on_load_project', 'on_pre_save_project', 'on_post_save_project', 'on_pre_close_project'):
        _call_listeners(name, [w])
        return None
    if name == 'on_close' and view in _view_listeners:
        _call_listeners(name, [v], view)
        _view_listeners.pop(view, None)
        return None
    if name == 'on_query_context':
        for r in _call_listeners(name, [v] + args, view, collect=True):
            if r is not None:
                return bool(r)
        return None
    if name == 'on_query_completions':
        out = []
        flags = 0
        for r in _call_listeners(name, [v] + args, view, collect=True):
            items = r
            if isinstance(r, inks.CompletionList):
                items = r.completions or []
                flags |= r.flags
            elif isinstance(r, tuple):
                items, f = r
                flags |= f
            for it in items or []:
                if isinstance(it, inks.CompletionItem):
                    out.append({'label': it.trigger, 'apply': it.completion, 'detail': it.annotation,
                                'snippet': it.completion_format == inks.COMPLETION_FORMAT_SNIPPET})
                elif isinstance(it, (list, tuple)):
                    trig = str(it[0])
                    label, _, detail = trig.partition('\t')
                    out.append({'label': label, 'apply': str(it[1]) if len(it) > 1 else label, 'detail': detail,
                                'snippet': '$' in (str(it[1]) if len(it) > 1 else '')})
                else:
                    out.append({'label': str(it), 'apply': str(it)})
        return {'items': out, 'flags': flags}
    if name == 'on_hover':
        _call_listeners(name, [v] + args, view)
        return None
    if name in ('on_text_command', 'on_window_command'):
        for r in _call_listeners(name, [v or w] + args, view, collect=True):
            if r:
                return list(r)
        return None
    _call_listeners(name, [v] if v else [], view)
    return None


# ---------------------------------------------------------------------------
# Console

_console_ns = {'inks': inks, 'inks_plugin': sys.modules[__name__]}
for _legacy, _own in compat.LEGACY_MODULES.items():
    _console_ns[_legacy] = _console_ns[_own]
_console = code.InteractiveInterpreter(_console_ns)


@_host.handler('eval')
def console_eval(source, window=None, view=None):
    _console_ns['window'] = inks.Window(window) if window else None
    _console_ns['view'] = inks.View(view) if view else None
    try:
        compiled = code.compile_command(source, '<console>', 'single')
    except SyntaxError:
        traceback.print_exc(limit=0)
        return True
    if compiled is None:
        # Incomplete input (e.g. a block opener): run as a statement block.
        compiled = compile(source, '<console>', 'exec')
    _console.runcode(compiled)
    return True
