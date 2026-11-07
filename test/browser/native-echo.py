#!/usr/bin/python3
"""Measure isolated real-PTY echo in GTK WebKit using synthetic key events."""
import base64
import json
import os
from pathlib import Path
import sys
import gi
gi.require_version('Gtk', '4.0')
gi.require_version('WebKit', '6.0')
from gi.repository import Gtk, WebKit, GLib

cfg = {key: os.environ['DFB_' + key.upper()] for key in ('token', 'sid', 'wid')}
script = Path(os.environ.get('DFB_SCRIPT', str(Path(__file__).with_suffix('.js'))))
source = script.read_text().replace('__DF_CONFIG__', base64.b64encode(json.dumps(cfg).encode()).decode())
app = Gtk.Application(application_id='org.darkfactory.NativeEchoProbe')
result = {'ok': False, 'error': 'timeout'}

def activate(application):
    manager = WebKit.UserContentManager()
    manager.add_script(WebKit.UserScript.new(source, WebKit.UserContentInjectedFrames.TOP_FRAME, WebKit.UserScriptInjectionTime.START, None, None))
    view = WebKit.WebView(user_content_manager=manager)
    window = Gtk.ApplicationWindow(application=application)
    window.set_default_size(1280, 900)
    window.set_title('DarkFactory isolated terminal test - click to focus')
    window.set_child(view)
    window_state = {'active': False, 'mapped': False}
    started = {'value': False}
    def start_load():
        if not started['value']:
            started['value'] = True
            view.load_uri(os.environ['DFB_BASE'] + '/?debug=0&panel=0')
    def active_changed(win, _param):
        window_state['active'] = win.is_active()
        window_state['mapped'] = win.get_mapped()
        if window_state['active'] and os.environ.get('DFB_REQUIRE_ACTIVE') == '1':
            start_load()
    window.connect('notify::is-active', active_changed)
    def title_changed(webview, _param):
        title = webview.get_title() or ''
        if title.startswith('DFPROBE:'):
            try:
                result.clear()
                result.update(json.loads(title[len('DFPROBE:'):]))
            except ValueError as error:
                result.update(ok=False, error=str(error))
            result['window'] = {**window_state, 'visible': window.get_visible(), 'mapped': window.get_mapped()}
            application.quit()
    view.connect('notify::title', title_changed)
    window.present()
    if os.environ.get('DFB_REQUIRE_ACTIVE') != '1':
        start_load()
    GLib.timeout_add_seconds(90, lambda: (application.quit(), False)[1])

app.connect('activate', activate)
app.run([])
print(json.dumps(result))
sys.exit(0 if result.get('ok') and (os.environ.get('DFB_SCRIPT') or len(result.get('samples', [])) == 16) else 1)
