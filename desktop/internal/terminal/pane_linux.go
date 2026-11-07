//go:build linux && cgo

// Package terminal embeds a VTE terminal in the desktop window.
//
// The VTE widget is an overlay child of the window, positioned over the web
// terminal's rectangle. It runs the same `darkfactory native-attach` client the
// Ghostty window uses, with a single-use pane capability instead of the master
// token. Input, resize and detach therefore stay on the audited attach path;
// only the emulator changes.
package terminal

/*
#cgo pkg-config: gtk4 vte-2.91-gtk4
#include <gtk/gtk.h>
#include <gdk/gdkkeysyms.h>
#include <vte/vte.h>
#include <fcntl.h>
#include <unistd.h>
#include <string.h>
#include <stdio.h>

typedef struct {
	GtkWidget *term;
	GtkWidget *popover;
	GtkWidget *copy;
} term_menu;

static GtkWidget *df_overlay = NULL;
static GtkWidget *df_holder = NULL;
static GtkWidget *df_term = NULL;
static term_menu *df_menu = NULL;

static void menu_copy(GtkButton *button, gpointer data) {
	term_menu *m = data;
	vte_terminal_copy_clipboard_format(VTE_TERMINAL(m->term), VTE_FORMAT_TEXT);
	gtk_popover_popdown(GTK_POPOVER(m->popover));
}

static void menu_paste(GtkButton *button, gpointer data) {
	term_menu *m = data;
	vte_terminal_paste_clipboard(VTE_TERMINAL(m->term));
	gtk_popover_popdown(GTK_POPOVER(m->popover));
}

static void menu_right_click(GtkGestureClick *gesture, int n_press, double x, double y, gpointer data) {
	term_menu *m = data;
	gtk_widget_set_sensitive(m->copy, vte_terminal_get_has_selection(VTE_TERMINAL(m->term)));
	GdkRectangle rect = {(int)x, (int)y, 1, 1};
	gtk_popover_set_pointing_to(GTK_POPOVER(m->popover), &rect);
	gtk_popover_popup(GTK_POPOVER(m->popover));
}

static gboolean menu_key(GtkEventControllerKey *controller, guint keyval, guint keycode,
	GdkModifierType state, gpointer data) {
	term_menu *m = data;
	if (!(state & GDK_CONTROL_MASK) || !(state & GDK_SHIFT_MASK) || (state & GDK_ALT_MASK)) {
		return FALSE;
	}
	if (keyval == GDK_KEY_c || keyval == GDK_KEY_C) {
		vte_terminal_copy_clipboard_format(VTE_TERMINAL(m->term), VTE_FORMAT_TEXT);
		return TRUE;
	}
	if (keyval == GDK_KEY_v || keyval == GDK_KEY_V) {
		vte_terminal_paste_clipboard(VTE_TERMINAL(m->term));
		return TRUE;
	}
	return FALSE;
}

static void setup_terminal_menu(GtkWidget *term) {
	term_menu *m = g_new0(term_menu, 1);
	m->term = term;

	m->popover = gtk_popover_new();
	GtkWidget *box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 2);
	m->copy = gtk_button_new_with_label("Copy");
	gtk_widget_set_sensitive(m->copy, FALSE);
	GtkWidget *paste = gtk_button_new_with_label("Paste");
	gtk_widget_add_css_class(m->copy, "flat");
	gtk_widget_add_css_class(paste, "flat");
	gtk_box_append(GTK_BOX(box), m->copy);
	gtk_box_append(GTK_BOX(box), paste);
	gtk_popover_set_child(GTK_POPOVER(m->popover), box);
	gtk_widget_set_parent(m->popover, term);
	df_menu = m;
	g_signal_connect(m->copy, "clicked", G_CALLBACK(menu_copy), m);
	g_signal_connect(paste, "clicked", G_CALLBACK(menu_paste), m);

	GtkGesture *click = gtk_gesture_click_new();
	gtk_gesture_single_set_button(GTK_GESTURE_SINGLE(click), GDK_BUTTON_SECONDARY);
	g_signal_connect(click, "pressed", G_CALLBACK(menu_right_click), m);
	gtk_widget_add_controller(term, GTK_EVENT_CONTROLLER(click));

	GtkEventController *keys = gtk_event_controller_key_new();
	gtk_event_controller_set_propagation_phase(keys, GTK_PHASE_CAPTURE);
	g_signal_connect(keys, "key-pressed", G_CALLBACK(menu_key), m);
	gtk_widget_add_controller(term, keys);
}

static void df_place(int x, int y, int w, int h) {
	if (df_holder == NULL) {
		return;
	}
	if (x < 0) x = 0;
	if (y < 0) y = 0;
	if (w < 40) w = 40;
	if (h < 30) h = 30;
	gtk_widget_set_margin_start(df_holder, x);
	gtk_widget_set_margin_top(df_holder, y);
	gtk_widget_set_size_request(df_holder, w, h);
}

// An overlay child paints above the WebView, so a modal HTML dialog under the
// pane is invisible and unclickable. Hiding the holder (not detaching it) lets
// the dialog through while the PTY stays attached and VTE keeps its screen;
// hiding the focused widget also hands keyboard focus back to the WebView.
static void df_set_visible(int visible) {
	if (df_holder == NULL) {
		return;
	}
	gtk_widget_set_visible(df_holder, visible ? TRUE : FALSE);
	if (visible && df_term != NULL) {
		gtk_widget_grab_focus(df_term);
	}
}

static int df_ensure_overlay(GtkWindow *win) {
	if (df_overlay != NULL) {
		return 0;
	}
	GtkWidget *child = gtk_window_get_child(win);
	if (child == NULL) {
		return 1;
	}
	// Keep the whole Wails tree (box + WebView) as the overlay's main child.
	g_object_ref(child);
	gtk_window_set_child(win, NULL);
	GtkWidget *overlay = gtk_overlay_new();
	gtk_overlay_set_child(GTK_OVERLAY(overlay), child);
	g_object_unref(child);
	gtk_window_set_child(win, overlay);
	df_overlay = overlay;
	return 0;
}

static void df_spawn_done(VteTerminal *term, GPid pid, GError *error, gpointer data) {
	int fd = GPOINTER_TO_INT(data);
	if (fd > 0) {
		close(fd);
	}
	if (error != NULL) {
		fprintf(stderr, "darkfactory terminal: %s\n", error->message);
		g_error_free(error);
	}
}

static void df_terminal_close(void) {
	// The menu popover is a child of the terminal. Remove it first, otherwise
	// VTE finalises with a live child and GTK warns.
	if (df_menu != NULL) {
		if (df_menu->popover != NULL) {
			gtk_widget_unparent(df_menu->popover);
		}
		g_free(df_menu);
		df_menu = NULL;
	}
	if (df_holder != NULL) {
		gtk_widget_unparent(df_holder);
		df_holder = NULL;
		df_term = NULL;
	}
}

static int df_terminal_open(GtkWindow *win, const char *daemon, const char *attach_bin,
	const char *session, const char *pane, int cols, int rows,
	int x, int y, int w, int h) {
	df_terminal_close();
	if (df_ensure_overlay(win) != 0) {
		fprintf(stderr, "darkfactory terminal: window has no child\n");
		return 1;
	}

	GtkWidget *holder = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
	gtk_widget_set_halign(holder, GTK_ALIGN_START);
	gtk_widget_set_valign(holder, GTK_ALIGN_START);
	gtk_widget_set_hexpand(holder, FALSE);
	gtk_widget_set_vexpand(holder, FALSE);
	gtk_widget_set_overflow(holder, GTK_OVERFLOW_HIDDEN);
	gtk_widget_add_css_class(holder, "df-terminal");

	GtkWidget *term = vte_terminal_new();
	gtk_widget_set_hexpand(term, TRUE);
	gtk_widget_set_vexpand(term, TRUE);
	// A small initial grid keeps the natural size below the requested
	// rectangle, so the holder gets exactly the measured bounds and the
	// terminal reflows to the allocated size.
	vte_terminal_set_size(VTE_TERMINAL(term), 20, 5);
	setup_terminal_menu(term);
	gtk_box_append(GTK_BOX(holder), term);
	gtk_overlay_add_overlay(GTK_OVERLAY(df_overlay), holder);
	df_holder = holder;
	df_term = term;
	df_place(x, y, w, h);
	gtk_widget_grab_focus(term);

	// The pane capability travels on fd 3, never argv: argv is world-readable
	// in /proc. The pipe is closed once the child has been spawned.
	int fds[2];
	if (pipe(fds) != 0) {
		fprintf(stderr, "darkfactory terminal: pipe failed\n");
		return 2;
	}
	size_t len = strlen(pane);
	if (write(fds[1], pane, len) != (ssize_t)len) {
		fprintf(stderr, "darkfactory terminal: pane write failed\n");
		close(fds[0]);
		close(fds[1]);
		return 3;
	}
	close(fds[1]);
	fcntl(fds[0], F_SETFD, FD_CLOEXEC);

	const char *argv[] = {
		attach_bin, "native-attach",
		"--listen", daemon,
		"--credential-fd", "3",
		session, NULL};
	int map[] = {3};
	const char *envv[] = {NULL};
	vte_terminal_spawn_with_fds_async(VTE_TERMINAL(term), VTE_PTY_DEFAULT, NULL,
		argv, envv, &fds[0], 1, map, 1, G_SPAWN_DEFAULT, NULL, NULL, NULL, -1,
		NULL, df_spawn_done, GINT_TO_POINTER(fds[0]));
	// Do not force the browser's cols/rows here. The web grid is measured with
	// xterm.js font metrics, which do not match VTE's cell box; applying it as
	// a VTE size made the widget's natural size exceed the measured rectangle
	// (clipped rows) or leave the allocated area partly unpainted. VTE derives
	// its grid from the allocated pixels below, and the attach client reads that
	// through its own PTY and posts the matching size to the daemon.
	(void)cols;
	(void)rows;
	return 0;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// Bounds is the web terminal rectangle in window (logical, CSS) pixels.
type Bounds struct {
	X int
	Y int
	W int
	H int
}

// Open replaces any open terminal with one attached to the given pane.
func Open(win unsafe.Pointer, daemon, attachBin, session, pane string, cols, rows int, b Bounds) error {
	if win == nil {
		return errors.New("native window is not available")
	}
	if daemon == "" || attachBin == "" || session == "" || pane == "" {
		return errors.New("terminal needs a daemon, attach binary, session and pane")
	}
	cDaemon := C.CString(daemon)
	cBin := C.CString(attachBin)
	cSession := C.CString(session)
	cPane := C.CString(pane)
	defer C.free(unsafe.Pointer(cDaemon))
	defer C.free(unsafe.Pointer(cBin))
	defer C.free(unsafe.Pointer(cSession))
	defer C.free(unsafe.Pointer(cPane))
	if rc := C.df_terminal_open((*C.GtkWindow)(win), cDaemon, cBin, cSession, cPane,
		C.int(cols), C.int(rows), C.int(b.X), C.int(b.Y), C.int(b.W), C.int(b.H)); rc != 0 {
		return fmt.Errorf("terminal open failed (%d)", int(rc))
	}
	return nil
}

// Place moves and resizes the open terminal without reattaching.
func Place(b Bounds) {
	C.df_place(C.int(b.X), C.int(b.Y), C.int(b.W), C.int(b.H))
}

// SetVisible hides or shows the pane without detaching it. The page hides it
// while a modal dialog is up: a GTK overlay child paints above the WebView, so
// the dialog would otherwise be covered, invisible and unclickable.
func SetVisible(visible bool) {
	v := C.int(0)
	if visible {
		v = 1
	}
	C.df_set_visible(v)
}

// Prepare wraps the window's WebView tree in the overlay while the window is
// still starting, before a later reparent could disturb a loaded page.
func Prepare(win unsafe.Pointer) error {
	if win == nil {
		return errors.New("native window is not available")
	}
	if rc := C.df_ensure_overlay((*C.GtkWindow)(win)); rc != 0 {
		return fmt.Errorf("terminal overlay failed (%d)", int(rc))
	}
	return nil
}

// Close destroys the terminal; the attach client sees its PTY close and exits.
func Close() {
	C.df_terminal_close()
}
