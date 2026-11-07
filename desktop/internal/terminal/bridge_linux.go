//go:build linux && cgo

package terminal

/*
#cgo pkg-config: gtk4 webkitgtk-6.0
#include <gtk/gtk.h>
#include <webkit/webkit.h>
#include <jsc/jsc.h>

extern void dfGoMessage(char *json);

static WebKitWebView *df_find_webview(GtkWidget *widget) {
	if (widget == NULL) {
		return NULL;
	}
	if (WEBKIT_IS_WEB_VIEW(widget)) {
		return WEBKIT_WEB_VIEW(widget);
	}
	GtkWidget *child = gtk_widget_get_first_child(widget);
	for (; child != NULL; child = gtk_widget_get_next_sibling(child)) {
		WebKitWebView *found = df_find_webview(child);
		if (found != NULL) {
			return found;
		}
	}
	return NULL;
}

static gboolean df_bridge_connected = FALSE;
static gboolean df_load_connected = FALSE;

static void df_inject_bridge(WebKitWebView *view) {
	const char *js =
		"window.__dfNativeSend=function(m){try{window.webkit.messageHandlers.dfNative.postMessage(m)}catch(e){}};";
	webkit_web_view_evaluate_javascript(view, js, -1, NULL, NULL, NULL, NULL, NULL);
}

static void df_bridge_load_changed(WebKitWebView *view, WebKitLoadEvent event, gpointer data) {
	if (event == WEBKIT_LOAD_FINISHED) {
		df_inject_bridge(view);
	}
}

static void df_bridge_message(WebKitUserContentManager *manager, JSCValue *value, gpointer data) {
	char *json = jsc_value_to_json(value, 0);
	if (json != NULL) {
		dfGoMessage(json);
		g_free(json);
	}
}

static int df_bridge_attach(GtkWindow *win) {
	WebKitWebView *view = df_find_webview(GTK_WIDGET(win));
	if (view == NULL) {
		return 1;
	}
	WebKitUserContentManager *manager = webkit_web_view_get_user_content_manager(view);
	if (!df_bridge_connected) {
		// Connect before registering: the docs warn that a message sent
		// between the two would otherwise be lost.
		g_signal_connect(manager, "script-message-received::dfNative", G_CALLBACK(df_bridge_message), NULL);
		if (!webkit_user_content_manager_register_script_message_handler(manager, "dfNative", NULL)) {
			return 2;
		}
		df_bridge_connected = TRUE;
	}
	if (!df_load_connected) {
		// A reload drops the injected function; reinstall it after every load.
		g_signal_connect(view, "load-changed", G_CALLBACK(df_bridge_load_changed), NULL);
		df_load_connected = TRUE;
	}
	df_inject_bridge(view);
	return 0;
}
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"
)

var (
	bridgeMu sync.Mutex
	bridgeCB func(string)
)

// OnMessage receives the JSON messages the page posts through the WebView
// script-message handler. It runs on the GTK main thread.
func OnMessage(cb func(string)) {
	bridgeMu.Lock()
	bridgeCB = cb
	bridgeMu.Unlock()
}

//export dfGoMessage
func dfGoMessage(raw *C.char) {
	message := C.GoString(raw)
	bridgeMu.Lock()
	cb := bridgeCB
	bridgeMu.Unlock()
	if cb != nil {
		cb(message)
	}
}

// AttachBridge registers the page-to-shell message channel and injects the
// bridge function. Call it after the window exists and on every load.
func AttachBridge(win unsafe.Pointer) error {
	if win == nil {
		return errors.New("native window is not available")
	}
	if rc := C.df_bridge_attach((*C.GtkWindow)(win)); rc != 0 {
		return errors.New("WebView bridge unavailable")
	}
	return nil
}
