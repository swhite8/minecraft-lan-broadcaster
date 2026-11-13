#!/usr/bin/env python3
"""GTK desktop controls for the Minecraft LAN Broadcaster user service."""
import json
import os
import re
from pathlib import Path
import subprocess
import sys
import threading

import gi
gi.require_version('Gtk', '4.0')
from gi.repository import Gtk, Gdk, GLib, Gio

ROOT = Path(__file__).resolve().parents[1]
UNIT = 'minecraft-lan-broadcaster-prototype.service'
SETTINGS = Path(os.environ.get('XDG_CONFIG_HOME', str(Path.home()/'.config'))) / 'minecraft-lan-broadcaster/settings.json'
SMOKE = '--smoke-test' in sys.argv
CSS = '''
window { background: #0c1915; color: #e6f2ee; }
label { color: #e6f2ee; }
.brand { color: #6fe4a9; font-size: 12px; font-weight: 700; letter-spacing: 2px; }
.title { font-size: 27px; font-weight: 700; }
.muted { color: #9ab1a9; }
.card { background: #172a22; border-radius: 16px; padding: 20px; }
.section { color: #6fe4a9; font-size: 11px; font-weight: 700; letter-spacing: 1px; }
.edition { font-size: 16px; font-weight: 600; }
entry { background: #0e2019; color: #e6f2ee; border: 1px solid #345344; border-radius: 9px; padding: 10px; min-height: 23px; }
button { background: #274136; color: #e6f2ee; border: 0; border-radius: 10px; padding: 11px 16px; box-shadow: none; }
button:hover { background: #345746; }
button.primary { background: #6fe4a9; color: #0c1915; font-weight: 700; min-height: 26px; }
button.primary label { color: #0c1915; }
button:disabled { opacity: .42; }
switch:checked { background: #6fe4a9; }
.status { color: #6fe4a9; font-size: 16px; font-weight: 600; }
textview, textview text { background: #102219; color: #b5cfc1; font-family: monospace; font-size: 11px; }
'''

def command(args, check=False):
    result = subprocess.run(args, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=15)
    if check and result.returncode:
        raise RuntimeError((result.stderr or result.stdout).strip() or 'Command failed')
    return result

def valid_target(value):
    try:
        host, port = value.strip().rsplit(':', 1)
        return bool(host) and not any(c.isspace() or c in '/\\' for c in host) and 1 <= int(port) <= 65535
    except (ValueError, TypeError):
        return False

class Broadcaster(Gtk.Application):
    def __init__(self):
        super().__init__(application_id='org.minecraftlan.broadcaster.desktop', flags=Gio.ApplicationFlags.NON_UNIQUE if SMOKE else Gio.ApplicationFlags.DEFAULT_FLAGS)
        self.busy = False
        self.running = False
        self.ready = False
        self.last_error = ''
        self.polling = False
        self.start_pending = False
        self.controls = []

    def label(self, text, style=None, wrap=False):
        widget = Gtk.Label(label=text, xalign=0)
        widget.set_wrap(wrap)
        if style: widget.add_css_class(style)
        return widget

    def button(self, text, callback, primary=False):
        widget = Gtk.Button(label=text)
        if primary: widget.add_css_class('primary')
        widget.connect('clicked', callback)
        return widget

    def do_activate(self):
        if self.get_active_window():
            self.get_active_window().present()
            return
        provider = Gtk.CssProvider()
        provider.load_from_data(CSS.encode())
        Gtk.StyleContext.add_provider_for_display(Gdk.Display.get_default(), provider, Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
        self.window = Gtk.ApplicationWindow(application=self, title='Minecraft LAN Broadcaster')
        self.window.set_default_size(560, 900)
        self.window.set_size_request(460, 650)
        scroll = Gtk.ScrolledWindow(hscrollbar_policy=Gtk.PolicyType.NEVER)
        self.window.set_child(scroll)
        content = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=17)
        for edge in ['top','bottom','start','end']: getattr(content, 'set_margin_'+edge)(26)
        scroll.set_child(content)
        heading = Gtk.Box(spacing=14)
        icon = Gtk.Image.new_from_file(str(ROOT/'assets/minecraft-lan-broadcaster-icon.png'))
        icon.set_pixel_size(58)
        heading.append(icon)
        titles = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=5)
        titles.append(self.label('MINECRAFT  /  LAN BROADCASTER', 'brand'))
        titles.append(self.label('LAN broadcast controls', 'title'))
        heading.append(titles)
        content.append(heading)
        content.append(self.label('Make your server appear on the local network for Bedrock consoles and Java computers.', 'muted', True))
        card = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=12)
        card.add_css_class('card')
        content.append(card)
        card.append(self.label('EDITIONS & DESTINATIONS', 'section'))
        settings = {'bedrock': True, 'java': False, 'target':'192.0.2.10:19132', 'java_target':'192.0.2.10:25565'}
        try: settings.update(json.loads(SETTINGS.read_text()))
        except (OSError, ValueError): pass
        self.bedrock, self.target = self.edition(card, 'Bedrock Edition', 'Console LAN discovery', settings['bedrock'], settings['target'])
        self.java, self.java_target = self.edition(card, 'Java Edition', 'LAN discovery with a local relay', settings['java'], settings['java_target'])
        choices = Gtk.Box(spacing=10, homogeneous=True)
        for label, host in [('Home network','192.0.2.10'),('Public domain','198.51.100.10')]:
            b = self.button(label, lambda _b, h=host: self.preset(h))
            choices.append(b)
            self.controls.append(b)
        card.append(choices)
        self.status = self.label('Checking broadcaster…', 'status')
        content.append(self.status)
        buttons = Gtk.Box(spacing=10, homogeneous=True)
        self.start = self.button('Start LAN broadcast', self.start_broadcast, True)
        self.stop = self.button('Stop broadcast', self.stop_broadcast)
        buttons.append(self.start); buttons.append(self.stop)
        content.append(buttons)
        content.append(self.label('Bedrock: join “Minecraft LAN Broadcaster”.\nJava: join “Minecraft LAN Broadcaster - Java”.\n\nKeep this computer and players on the same local network. Broadcasting continues when you close this window. Stopping disconnects Java players using the relay.', 'muted', True))
        content.append(self.label('No ads · No account sign-in · No root', 'section'))
        details = Gtk.Expander(label='Connection details')
        self.logs = Gtk.TextView(editable=False, cursor_visible=False, wrap_mode=Gtk.WrapMode.WORD_CHAR)
        pane = Gtk.ScrolledWindow(min_content_height=180)
        pane.set_child(self.logs)
        details.set_child(pane)
        content.append(details)
        self.bedrock.connect('notify::active', lambda *_: self.update_controls())
        self.java.connect('notify::active', lambda *_: self.update_controls())
        self.window.present()
        if SMOKE:
            self.running = False
            self.status.set_text('Ready to broadcast')
            self.update_controls()
            GLib.timeout_add(1000, self.smoke)
        else:
            self.refresh()
            GLib.timeout_add(1500, self.refresh)

    def edition(self, card, title, subtitle, active, target):
        row = Gtk.Box(spacing=12)
        title_box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=3, hexpand=True)
        title_box.append(self.label(title, 'edition'))
        title_box.append(self.label(subtitle, 'muted'))
        row.append(title_box)
        toggle = Gtk.Switch(active=active, valign=Gtk.Align.CENTER)
        row.append(toggle)
        card.append(row)
        entry = Gtk.Entry(text=target, hexpand=True)
        card.append(entry)
        self.controls.append(toggle)
        return toggle, entry

    def preset(self, host):
        self.target.set_text(host+':19132')
        self.java_target.set_text(host+':25565')

    def update_controls(self):
        locked = self.busy or self.running
        for widget in self.controls: widget.set_sensitive(not locked)
        self.target.set_sensitive(not locked and self.bedrock.get_active())
        self.java_target.set_sensitive(not locked and self.java.get_active())
        self.start.set_sensitive(not locked)
        self.stop.set_sensitive(self.running and not self.busy)
        self.start.set_label('Broadcast is running' if self.running else 'Start LAN broadcast')

    def background(self, action):
        def run():
            try: action()
            except Exception as e: GLib.idle_add(self.failed, str(e))
            else: GLib.idle_add(self.finished)
        self.busy = True
        self.update_controls()
        threading.Thread(target=run, daemon=True).start()

    def failed(self, error):
        self.start_pending = False
        self.last_error = error
        self.busy = False
        self.status.set_text('Could not change broadcast: '+error)
        self.update_controls()
        return False

    def finished(self):
        self.last_error = ''
        self.busy = False
        self.refresh()
        return False

    def start_broadcast(self, _button):
        b, j = self.bedrock.get_active(), self.java.get_active()
        target, java_target = self.target.get_text().strip(), self.java_target.get_text().strip()
        if not (b or j): self.failed('Select at least one edition.'); return
        if (b and not valid_target(target)) or (j and not valid_target(java_target)):
            self.failed('Enter a destination as host:port (port 1–65535).'); return
        settings = dict(bedrock=b, java=j, target=target, java_target=java_target)
        self.start_pending = True
        self.status.set_text('Starting…')
        def action():
            SETTINGS.parent.mkdir(parents=True, exist_ok=True)
            SETTINGS.write_text(json.dumps(settings, indent=2)+'\n')
            args = ['systemd-run','--user','--unit='+UNIT.removesuffix('.service'),'--collect',str(ROOT/'dist/minecraft-lan-broadcaster'),'-bedrock='+str(b).lower(),'-java='+str(j).lower(),'-target',target,'-java-target',java_target,'-java-port','25566']
            command(args, check=True)
        self.background(action)

    def stop_broadcast(self, _button):
        self.status.set_text('Stopping…')
        self.background(lambda: command(['systemctl','--user','stop',UNIT], check=True))

    def refresh(self):
        if self.polling: return True
        self.polling = True
        def run():
            try:
                state = command(['systemctl','--user','show',UNIT,'--property=ActiveState','--property=ExecStart']).stdout
                logs = command(['journalctl','--user','-u',UNIT,'-n','65','--no-pager','-o','cat']).stdout
                GLib.idle_add(self.render_state, state, logs)
            except Exception as e: GLib.idle_add(self.render_state, '', str(e))
        threading.Thread(target=run, daemon=True).start()
        return True

    def render_state(self, state, logs):
        self.polling = False
        self.running = 'ActiveState=active' in state or 'ActiveState=activating' in state
        if not self.busy:
            # When attaching to a service started outside this GUI, reflect its actual edition flags.
            if self.running:
                self.bedrock.set_active('-bedrock=false' not in state)
                self.java.set_active('-java=true' in state)
                for flag, entry in [('target', self.target), ('java-target', self.java_target)]:
                    match = re.search(r'(?:^|\s)-'+flag+r'(?:=|\s+)([^;\s]+)', state)
                    if match: entry.set_text(match.group(1))
            if self.start_pending and not self.running:
                self.last_error = 'Broadcast could not start. Open Connection details for the error.'
                self.status.set_text(self.last_error)
            self.start_pending = False
            if not self.last_error:
                self.status.set_text('● Broadcasting on your local network' if self.running else 'Ready to broadcast')
        self.logs.get_buffer().set_text(logs[-24000:] or 'Connection events will appear here.')
        self.update_controls()
        return False

    def smoke(self):
        (ROOT/'dist').mkdir(exist_ok=True)
        command(['import', '-window', 'root', '-trim', str(ROOT/'dist/linux-gui-screenshot.png')], check=True)
        assert valid_target('192.0.2.10:19132')
        assert not valid_target('bad:99999')
        assert not valid_target('https://bad:19132')
        self.bedrock.set_active(False); self.java.set_active(True)
        self.update_controls()
        assert self.java_target.get_sensitive() and not self.target.get_sensitive()
        self.bedrock.set_active(True)
        self.preset('192.0.2.10')
        print('GUI smoke test passed', flush=True)
        self.quit()
        return False

if __name__ == '__main__':
    Broadcaster().run([sys.argv[0]])
