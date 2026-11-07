//go:build linux && cgo

package main

/*
#cgo pkg-config: gtk4 vte-2.91-gtk4 webkitgtk-6.0
#include <gtk/gtk.h>
#include <gdk/gdkkeysyms.h>
#include <vte/vte.h>
#include <webkit/webkit.h>
#include <fcntl.h>
#include <unistd.h>
#include <stdio.h>

typedef struct {
	GtkWidget *term;
	GtkWidget *popover;
	GtkWidget *copy;
} term_menu;

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

static GtkWidget *find_webview(GtkWidget *box) {
	GtkWidget *child = gtk_widget_get_first_child(box);
	while (child != NULL) {
		if (WEBKIT_IS_WEB_VIEW(child)) {
			return child;
		}
		child = gtk_widget_get_next_sibling(child);
	}
	return NULL;
}

static int attach_pane(GtkWindow *win, const char *daemon, const char *token_file,
	const char *attach_bin, const char *session) {
	GtkWidget *box = gtk_window_get_child(win);
	if (box == NULL) {
		fprintf(stderr, "spike: window has no child\n");
		return 1;
	}
	GtkWidget *webview = find_webview(box);
	if (webview == NULL) {
		fprintf(stderr, "spike: WebView not found in %s\n", G_OBJECT_TYPE_NAME(box));
		return 2;
	}
	// Keep the WebView alive across the removal, then hand it to the paned.
	g_object_ref(webview);
	gtk_box_remove(GTK_BOX(box), webview);

	GtkWidget *paned = gtk_paned_new(GTK_ORIENTATION_HORIZONTAL);
	gtk_widget_set_hexpand(paned, TRUE);
	gtk_widget_set_vexpand(paned, TRUE);
	gtk_paned_set_start_child(GTK_PANED(paned), webview);
	gtk_paned_set_resize_start_child(GTK_PANED(paned), TRUE);
	gtk_paned_set_shrink_start_child(GTK_PANED(paned), TRUE);

	GtkWidget *term = vte_terminal_new();
	gtk_widget_set_hexpand(term, TRUE);
	gtk_widget_set_vexpand(term, TRUE);
	gtk_widget_set_size_request(term, 480, -1);
	setup_terminal_menu(term);
	gtk_paned_set_end_child(GTK_PANED(paned), term);
	gtk_paned_set_resize_end_child(GTK_PANED(paned), TRUE);
	gtk_paned_set_shrink_end_child(GTK_PANED(paned), TRUE);

	gtk_box_append(GTK_BOX(box), paned);
	gtk_paned_set_position(GTK_PANED(paned), 760);

	// The pane is the emulator; a local PTY runs the existing attach client so
	// input, resize and detach stay on the audited path. The credential is a
	// spike-only token file mapped to fd 3; the real shell uses a ticket.
	int cred = open(token_file, O_RDONLY);
	if (cred >= 0) {
		fcntl(cred, F_SETFD, FD_CLOEXEC);
	}
	if (cred < 0) {
		fprintf(stderr, "spike: cannot open credential file\n");
	} else {
		const char *argv[] = {
			(char *)attach_bin, "native-attach",
			"--listen", (char *)daemon,
			"--credential-fd", "3",
			(char *)session, NULL};
		int fds[] = {cred};
		int map[] = {3};
		vte_terminal_set_size(VTE_TERMINAL(term), 100, 30);
		vte_terminal_spawn_with_fds_async(VTE_TERMINAL(term), VTE_PTY_DEFAULT, NULL,
			argv, NULL, fds, 1, map, 1, G_SPAWN_DEFAULT, NULL, NULL, NULL, -1,
			NULL, NULL, NULL);
	}
	fprintf(stderr, "spike: paned attached, webview=%s term=%s\n",
		G_OBJECT_TYPE_NAME(webview), G_OBJECT_TYPE_NAME(term));
	return 0;
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

func attachPane(win unsafe.Pointer, daemon, tokenFile, attachBin, session string) error {
	if win == nil {
		return errors.New("native window is not available")
	}
	cDaemon := C.CString(daemon)
	cToken := C.CString(tokenFile)
	cBin := C.CString(attachBin)
	cSession := C.CString(session)
	defer C.free(unsafe.Pointer(cDaemon))
	defer C.free(unsafe.Pointer(cToken))
	defer C.free(unsafe.Pointer(cBin))
	defer C.free(unsafe.Pointer(cSession))
	if rc := C.attach_pane((*C.GtkWindow)(win), cDaemon, cToken, cBin, cSession); rc != 0 {
		return errors.New("attach failed")
	}
	return nil
}
