//go:build linux && cgo && !gtk3

package main

/*
#cgo pkg-config: gtk4 webkitgtk-6.0
#include <gtk/gtk.h>
#include <webkit/webkit.h>
#include <stdlib.h>
#include <string.h>

static gboolean df_origin_allowed(const gchar *uri, const gchar *origin) {
	if (uri == NULL || origin == NULL) {
		return FALSE;
	}
	size_t n = strlen(origin);
	if (strncmp(uri, origin, n) != 0) {
		return FALSE;
	}
	char next = uri[n];
	return next == '\0' || next == '/' || next == '?' || next == '#';
}

static WebKitWebView *df_find_webview(GtkWidget *widget) {
	if (widget == NULL) {
		return NULL;
	}
	if (WEBKIT_IS_WEB_VIEW(widget)) {
		return WEBKIT_WEB_VIEW(widget);
	}
	GtkWidget *child = NULL;
	if (GTK_IS_WINDOW(widget)) {
		child = gtk_window_get_child(GTK_WINDOW(widget));
	} else {
		child = gtk_widget_get_first_child(widget);
	}
	for (; child != NULL; child = gtk_widget_get_next_sibling(child)) {
		WebKitWebView *found = df_find_webview(child);
		if (found != NULL) {
			return found;
		}
	}
	return NULL;
}

static const gchar *df_decision_uri(WebKitPolicyDecision *decision) {
	WebKitNavigationAction *action = NULL;
	if (WEBKIT_IS_NAVIGATION_POLICY_DECISION(decision)) {
		action = webkit_navigation_policy_decision_get_navigation_action(WEBKIT_NAVIGATION_POLICY_DECISION(decision));
	}
	if (action == NULL) {
		return NULL;
	}
	WebKitURIRequest *request = webkit_navigation_action_get_request(action);
	if (request == NULL) {
		return NULL;
	}
	return webkit_uri_request_get_uri(request);
}

static gboolean df_decide_policy(WebKitWebView *web_view, WebKitPolicyDecision *decision, WebKitPolicyDecisionType decision_type, gpointer user_data) {
	const gchar *origin = (const gchar *)user_data;
	const gchar *uri = df_decision_uri(decision);

	if (decision_type == WEBKIT_POLICY_DECISION_TYPE_NEW_WINDOW_ACTION) {
		g_printerr("darkfactory: blocked new window to %s\n", uri != NULL ? uri : "(null)");
		webkit_policy_decision_ignore(decision);
		return TRUE;
	}
	if (decision_type == WEBKIT_POLICY_DECISION_TYPE_NAVIGATION_ACTION) {
		if (!df_origin_allowed(uri, origin)) {
			g_printerr("darkfactory: blocked navigation to %s\n", uri != NULL ? uri : "(null)");
			webkit_policy_decision_ignore(decision);
			return TRUE;
		}
	}
	return FALSE;
}

typedef struct {
	GtkWidget *window;
	gchar *origin;
	int attempts;
} DFGuard;

static gboolean df_install_try(gpointer data) {
	DFGuard *guard = (DFGuard *)data;
	WebKitWebView *view = df_find_webview(guard->window);
	if (view == NULL) {
		guard->attempts--;
		if (guard->attempts > 0) {
			return G_SOURCE_CONTINUE;
		}
		g_printerr("darkfactory: navigation guard could not find the WebKit view\n");
		g_free(guard->origin);
		g_free(guard);
		return G_SOURCE_REMOVE;
	}
	g_printerr("darkfactory: navigation guard active\n");
	g_signal_connect_data(G_OBJECT(view), "decide-policy", G_CALLBACK(df_decide_policy),
		guard->origin, (GClosureNotify)g_free, 0);
	g_free(guard);
	return G_SOURCE_REMOVE;
}

static void df_install_navigation_guard(void *native, const char *origin) {
	DFGuard *guard = g_new0(DFGuard, 1);
	guard->window = GTK_WIDGET((GtkWindow *)native);
	guard->origin = g_strdup(origin);
	guard->attempts = 50;
	if (df_install_try(guard) == G_SOURCE_CONTINUE) {
		g_timeout_add(100, df_install_try, guard);
	}
}
*/
import "C"

import (
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

var navigationGuardInstalled bool

// installNavigationGuard confines every later top-level navigation to the
// validated loopback origin and refuses every new window. Wails v3.0.0 has no
// navigation hook, so this attaches the policy directly to the same WebKitGTK
// view the shell already owns. It exposes no method to page script.
func installNavigationGuard(window *application.WebviewWindow, origin string) bool {
	if navigationGuardInstalled {
		return true
	}
	if window == nil || origin == "" {
		return false
	}
	native := window.NativeWindow()
	if native == nil {
		return false
	}
	cOrigin := C.CString(origin)
	defer C.free(unsafe.Pointer(cOrigin))
	C.df_install_navigation_guard(native, cOrigin)
	navigationGuardInstalled = true
	return true
}
