package dev.darkfactory.mobile

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.app.AlertDialog
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.graphics.Insets
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.net.http.SslError
import android.os.Build
import android.os.Bundle
import android.text.InputType
import android.text.TextUtils
import android.view.Gravity
import android.view.ViewGroup
import android.view.WindowInsets
import android.webkit.CookieManager
import android.webkit.SslErrorHandler
import android.webkit.WebResourceRequest
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import android.window.OnBackInvokedCallback
import android.window.OnBackInvokedDispatcher

/**
 * A native Android shell for the DarkFactory UI. The UI bytes come from the
 * APK, the network client is native, and the page talks only to a loopback
 * origin owned by this process. There is deliberately no JavaScript bridge and
 * no token in WebView storage.
 *
 * The app keeps several paired daemons in one Keystore-backed registry. Only
 * one host is connected at a time, so the bundled page's session and worktree
 * ids never cross hosts; the previous loopback origin and WebView are destroyed
 * on a switch.
 */
class MainActivity : Activity() {
    private lateinit var tokenStore: TokenStore
    private lateinit var root: FrameLayout
    private var loopback: LoopbackServer? = null
    private var webView: WebView? = null
    private var proxyPort = 0
    private var originHost = ""
    private var currentHostId = ""
    private var connectGeneration = 0
    private var backCallback: OnBackInvokedCallback? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        tokenStore = TokenStore(this)
        root = FrameLayout(this).apply { setBackgroundColor(BACKGROUND) }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            @Suppress("DEPRECATION")
            window.setDecorFitsSystemWindows(false)
            val safeTypes = WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout()
            val handledTypes = safeTypes or WindowInsets.Type.ime()
            root.setOnApplyWindowInsetsListener { view, insets ->
                val safe = insets.getInsets(handledTypes)
                view.setPadding(safe.left, safe.top, safe.right, safe.bottom)
                // The native container owns spacing, including keyboard resize.
                // Forward zeroes so WebView cannot apply those insets twice.
                WindowInsets.Builder(insets)
                    .setInsets(handledTypes, Insets.NONE)
                    .setInsetsIgnoringVisibility(safeTypes, Insets.NONE)
                    .setDisplayCutout(null)
                    .build()
            }
        }
        setContentView(root)
        // targetSdk 35+ sends back gestures to the predictive back dispatcher,
        // not to onBackPressed. Register the same handler for API 33+.
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            val callback = OnBackInvokedCallback { handleBack() }
            backCallback = callback
            onBackInvokedDispatcher.registerOnBackInvokedCallback(
                OnBackInvokedDispatcher.PRIORITY_DEFAULT,
                callback
            )
        }

        if (!handlePairIntent(intent, initial = true)) {
            openInitial()
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handlePairIntent(intent, initial = false)
    }

    private fun openInitial() {
        val registry = tokenStore.loadRegistry()
        val active = registry.active
        if (active == null) {
            showPairing(null)
            return
        }
        if (!active.hasE2EE) {
            showHosts("This saved device predates app-layer encryption. Its pin is retained; explicitly Forget it to remove it, then pair again.")
            return
        }
        startWeb(active)
    }

    /**
     * The desktop QR is a custom-scheme deep link. It carries only the daemon
     * root and the one-time code; the user still confirms the host fingerprint
     * before the token is saved. An app that is already connected asks before
     * opening the pair form; existing hosts stay saved.
     */
    private fun handlePairIntent(intent: Intent?, initial: Boolean): Boolean {
        val data = intent?.data ?: return false
        if (data.scheme != "darkfactory" || data.host != "pair") return false
        val rawBase = data.getQueryParameter("base").orEmpty()
        val code = data.getQueryParameter("code").orEmpty()
        if (rawBase.isEmpty() || code.isEmpty()) {
            return pairIntentError(
                initial,
                "The pairing QR is incomplete. Generate a new code on the desktop."
            )
        }
        val base = try {
            NativePair.normalizeBase(rawBase)
        } catch (error: Exception) {
            return pairIntentError(
                initial,
                error.message ?: "The pairing QR has an invalid daemon URL."
            )
        }
        if (!initial && webView != null) {
            AlertDialog.Builder(this)
                .setTitle("Add this host?")
                .setMessage(
                    "Existing hosts stay saved. If this is a host you already paired, " +
                        "its token and pinned key are replaced after you confirm the fingerprint."
                )
                .setNegativeButton("Cancel", null)
                .setPositiveButton("Continue") { _, _ ->
                    showPairing("Scanned pairing link. Tap Pair and connect.", base, code)
                }
                .show()
            return true
        }
        showPairing("Scanned pairing link. Tap Pair and connect.", base, code)
        return true
    }

    private fun pairIntentError(initial: Boolean, message: String): Boolean {
        if (!initial && webView != null) {
            AlertDialog.Builder(this)
                .setTitle("Pairing link")
                .setMessage(message)
                .setPositiveButton("OK", null)
                .show()
        } else {
            showPairing(message)
        }
        return true
    }

    override fun onDestroy() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            backCallback?.let { onBackInvokedDispatcher.unregisterOnBackInvokedCallback(it) }
            backCallback = null
        }
        stopWeb()
        super.onDestroy()
    }

    override fun onRequestPermissionsResult(
        requestCode: Int,
        permissions: Array<out String>,
        grantResults: IntArray
    ) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode == REQUEST_NOTIFICATIONS &&
            grantResults.isNotEmpty() &&
            grantResults[0] == PackageManager.PERMISSION_GRANTED
        ) {
            NotificationService.start(this)
        }
    }

    private fun ensureNotificationPermission() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) return
        if (checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED) return
        requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), REQUEST_NOTIFICATIONS)
    }

    override fun onPause() {
        webView?.onPause()
        super.onPause()
    }

    override fun onResume() {
        super.onResume()
        webView?.onResume()
    }

    // API < 33. From 33 the callback registered in onCreate runs instead; lint
    // cannot see that, so this override is intentionally still here.
    @SuppressLint("GestureBackNavigation")
    @Suppress("DEPRECATION", "OVERRIDE_DEPRECATION")
    override fun onBackPressed() {
        handleBack()
    }

    @Suppress("DEPRECATION")
    private fun handleBack() {
        val web = webView
        if (web != null) {
            if (web.canGoBack()) {
                web.goBack()
            } else {
                showHosts(null)
            }
            return
        }
        super.onBackPressed()
    }

    private fun showPairing(
        message: String?,
        baseValue: String? = null,
        codeValue: String? = null
    ) {
        stopWeb()
        val form = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER_HORIZONTAL
            setPadding(dp(24), dp(48), dp(24), dp(24))
            setBackgroundColor(BACKGROUND)
        }

        form.addView(TextView(this).apply {
            text = "DarkFactory"
            setTextColor(TEXT)
            textSize = 28f
            gravity = Gravity.CENTER_HORIZONTAL
        }, matchWrap())

        form.addView(TextView(this).apply {
            text = "Pair this phone with a desktop daemon."
            setTextColor(MUTED)
            textSize = 14f
            gravity = Gravity.CENTER_HORIZONTAL
            setPadding(0, dp(8), 0, dp(28))
        }, matchWrap())

        val base = EditText(this).apply {
            hint = "https://desktop.tailnet.ts.net"
            setHintTextColor(MUTED)
            setTextColor(TEXT)
            isSingleLine = true
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI
            setText(baseValue.orEmpty())
        }
        form.addView(base, fieldParams())

        val code = EditText(this).apply {
            hint = "Pairing code"
            setHintTextColor(MUTED)
            setTextColor(TEXT)
            isSingleLine = true
            inputType = InputType.TYPE_CLASS_TEXT or
                InputType.TYPE_TEXT_VARIATION_VISIBLE_PASSWORD
            setText(codeValue.orEmpty())
        }
        form.addView(code, fieldParams())

        form.addView(TextView(this).apply {
            text = "Scan the QR from Pair phone, or enter the daemon URL and code. " +
                "Use HTTPS over Tailscale; plain http is accepted only on loopback."
            setTextColor(MUTED)
            textSize = 12f
            setPadding(0, dp(8), 0, dp(12))
        }, matchWrap())

        val pair = Button(this).apply {
            text = "Pair and connect"
            isAllCaps = false
            setTextColor(Color.WHITE)
            setBackgroundColor(ACCENT)
        }
        form.addView(pair, matchWrap())

        val status = TextView(this).apply {
            setTextColor(if (message?.startsWith("Scanned") == true) MUTED else ERROR)
            textSize = 13f
            setPadding(0, dp(14), 0, 0)
            text = message.orEmpty()
        }
        form.addView(status, matchWrap())

        if (tokenStore.loadRegistry().hosts.isNotEmpty()) {
            val back = Button(this).apply {
                text = "Back to hosts"
                isAllCaps = false
                setTextColor(TEXT)
                setBackgroundColor(CARD)
            }
            back.setOnClickListener { showHosts(null) }
            form.addView(back, fieldParams())
        }

        pair.setOnClickListener {
            val daemon = base.text.toString().trim()
            val pairingCode = code.text.toString().trim()
            if (daemon.isEmpty() || pairingCode.isEmpty()) {
                status.text = "Enter both the daemon URL and the pairing code."
                return@setOnClickListener
            }
            pair.isEnabled = false
            status.setTextColor(MUTED)
            status.text = "Checking the host key…"
            Thread({
                try {
                    val prepared = NativePair.prepare(
                        daemon,
                        pairingCode,
                        tokenStore.loadRegistry().hosts
                    )
                    runOnUiThread { confirmPairing(prepared, status, pair) }
                } catch (error: Exception) {
                    val detail = error.message
                    runOnUiThread {
                        status.setTextColor(ERROR)
                        status.text = if (detail.isNullOrEmpty()) "Pairing failed." else detail
                        pair.isEnabled = true
                    }
                }
            }, "darkfactory-pair").start()
        }

        root.removeAllViews()
        root.addView(
            form,
            FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT
            )
        )
    }

    private fun confirmPairing(
        prepared: NativePair.PreparedPair,
        status: TextView,
        pairButton: Button
    ) {
        if (prepared.fingerprint.isEmpty()) {
            status.setTextColor(ERROR)
            status.text = "This daemon did not provide a verifiable host key. Update it and pair again."
            pairButton.isEnabled = true
            return
        }
        status.text = ""
        AlertDialog.Builder(this)
            .setTitle("Verify the host key")
            .setMessage(
                "Compare this fingerprint with the Pair a phone dialog on the desktop:\n\n" +
                    prepared.fingerprint +
                    "\n\nSuite: " + prepared.suite +
                    "\n\nIf it does not match, cancel and pair again. A mismatched key means " +
                    "the network path is not the daemon you expect. The one-time code is sent only after you confirm."
            )
            .setCancelable(false)
            .setPositiveButton("Fingerprint matches") { _, _ ->
                status.setTextColor(MUTED)
                status.text = "Completing pairing…"
                Thread({
                    try {
                        val connection = NativePair.complete(
                            prepared,
                            tokenStore.loadRegistry().hosts
                        )
                        runOnUiThread {
                            try {
                                tokenStore.save(connection)
                                startWeb(connection)
                            } catch (error: Exception) {
                                status.setTextColor(ERROR)
                                status.text = "Could not protect the device token: ${error.message}"
                                pairButton.isEnabled = true
                            }
                        }
                    } catch (error: Exception) {
                        runOnUiThread {
                            val detail = error.message
                            status.setTextColor(ERROR)
                            status.text = if (detail.isNullOrEmpty()) "Pairing failed." else detail
                            pairButton.isEnabled = true
                        }
                    }
                }, "darkfactory-pair-complete").start()
            }
            .setNegativeButton("Cancel") { _, _ ->
                if (tokenStore.loadRegistry().hosts.isEmpty()) {
                    showPairing("Pairing cancelled. Verify the host key before trying again.")
                } else {
                    showHosts("Pairing cancelled. No host was added.")
                }
            }
            .show()
    }

    private fun startWeb(connection: TokenStore.Connection) {
        if (!connection.hasE2EE) {
            // The picker can still show a row that lost its E2EE fields (a
            // migration or a cold start from a pair deep link). Refuse before
            // opening any upstream connection: there is no plaintext path.
            showHosts("This saved host has no app-layer encryption key. Forget it and pair again.")
            return
        }
        stopWeb()
        val generation = connectGeneration
        showLoading(connection)
        Thread({
            val verified = try {
                NativePair.verifyHost(connection)
            } catch (error: NativePair.HostIdentityChangedException) {
                // Keep the pin. Forgetting it on the word of an unauthenticated
                // peer is exactly how a forced re-pair becomes a downgrade; the
                // user forgets and re-pairs deliberately from the host list.
                runOnUiThread {
                    if (generation != connectGeneration || isFinishing) return@runOnUiThread
                    showHosts(
                        error.message ?: "Host identity changed. Forget the host and pair again."
                    )
                }
                return@Thread
            } catch (error: Exception) {
                val detail = error.message ?: "The host could not be verified."
                runOnUiThread {
                    if (generation != connectGeneration || isFinishing) return@runOnUiThread
                    if (tokenStore.loadRegistry().hosts.isEmpty()) {
                        showPairing(detail)
                    } else {
                        showHosts("${labelOf(connection)} is offline: $detail")
                    }
                }
                return@Thread
            }
            runOnUiThread {
                if (generation != connectGeneration || isFinishing) return@runOnUiThread
                if (verified != connection) {
                    // The daemon rotated its X25519/ML-KEM half under the same
                    // pin; remember the new signed bundle for the next launch.
                    try {
                        tokenStore.save(verified)
                    } catch (_: Exception) {
                        // A stale bundle still works; verifyHost runs again.
                    }
                }
                startWebNow(verified)
            }
        }, "darkfactory-verify").start()
    }

    private fun showLoading(connection: TokenStore.Connection) {
        val box = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER
            setPadding(dp(24), dp(24), dp(24), dp(24))
            setBackgroundColor(BACKGROUND)
        }
        box.addView(TextView(this).apply {
            text = "Connecting to ${labelOf(connection)}…"
            setTextColor(TEXT)
            textSize = 18f
            gravity = Gravity.CENTER
        }, matchWrap())
        box.addView(TextView(this).apply {
            text = connection.base
            setTextColor(MUTED)
            textSize = 12f
            gravity = Gravity.CENTER
            setPadding(0, dp(8), 0, dp(20))
        }, matchWrap())
        box.addView(Button(this).apply {
            text = "Cancel"
            isAllCaps = false
            setTextColor(TEXT)
            setBackgroundColor(CARD)
            setOnClickListener { showHosts(null) }
        }, matchWrap())
        root.removeAllViews()
        root.addView(
            box,
            FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT
            )
        )
    }

    // These hardening flags are already false by default for a modern targetSdk,
    // but WebView defaults have changed across releases; keep them explicit.
    @Suppress("DEPRECATION")
    private fun startWebNow(connection: TokenStore.Connection) {
        val e2ee = try {
            E2EE.from(connection)
        } catch (error: Exception) {
            // Local key material is unusable. The row and its pin stay: only an
            // explicit Forget removes a host, so a corrupt file cannot be turned
            // into a re-pair by anything on the network.
            showHosts("The stored host key is not usable: ${error.message} Forget it and pair again.")
            return
        }
        val local = try {
            LoopbackServer(
                this,
                connection.base,
                e2ee,
                onLogout = { message ->
                    runOnUiThread {
                        if (currentHostId == connection.hostId) showHosts(message)
                    }
                }
            )
        } catch (error: Exception) {
            showHosts("Could not start the local client: ${error.message}")
            return
        }
        loopback = local
        originHost = local.originHost
        proxyPort = try {
            local.start()
        } catch (error: Exception) {
            local.stop()
            loopback = null
            showHosts("Could not start the local client: ${error.message}")
            return
        }
        currentHostId = connection.hostId
        try {
            tokenStore.setActive(connection.hostId)
            tokenStore.markSeen(connection.hostId)
        } catch (_: Exception) {
            // The host is usable for this session even if the last-used stamp
            // could not be rewritten.
        }
        ensureNotificationPermission()
        NotificationService.start(this)

        val web = WebView(this)
        web.setBackgroundColor(BACKGROUND)
        val settings = web.settings
        settings.javaScriptEnabled = true
        settings.domStorageEnabled = true
        settings.databaseEnabled = false
        settings.allowFileAccess = false
        settings.allowContentAccess = false
        settings.allowFileAccessFromFileURLs = false
        settings.allowUniversalAccessFromFileURLs = false
        settings.mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
        settings.mediaPlaybackRequiresUserGesture = true
        settings.javaScriptCanOpenWindowsAutomatically = false
        settings.setSupportMultipleWindows(false)
        settings.setSupportZoom(false)
        settings.builtInZoomControls = false
        settings.displayZoomControls = false
        settings.loadWithOverviewMode = false
        settings.useWideViewPort = true
        settings.setSaveFormData(false)
        settings.setGeolocationEnabled(false)
        settings.cacheMode = WebSettings.LOAD_NO_CACHE
        settings.userAgentString = settings.userAgentString + " DarkFactoryAndroid/0.1"
        CookieManager.getInstance().setAcceptCookie(false)
        WebView.setWebContentsDebuggingEnabled(false)

        web.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(
                view: WebView,
                request: WebResourceRequest
            ): Boolean {
                return !isLocalOrigin(request.url)
            }

            override fun onReceivedSslError(
                view: WebView,
                handler: SslErrorHandler,
                error: SslError
            ) {
                // The loopback origin is HTTP. A TLS error belongs to an
                // external navigation and is never bypassed.
                handler.cancel()
            }
        }
        web.setDownloadListener { _, _, _, _, _ ->
            // Downloads are not part of the client contract. Never hand a URL
            // to an external application from the trusted page.
        }

        webView = web
        root.removeAllViews()
        root.addView(
            webShell(connection, web),
            FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT
            )
        )
        web.loadUrl("http://$originHost:$proxyPort/")
    }

    private fun webShell(connection: TokenStore.Connection, web: WebView): LinearLayout {
        val shell = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(BACKGROUND)
        }
        val bar = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(dp(14), 0, dp(8), 0)
            background = cardBackground()
        }
        bar.addView(TextView(this).apply {
            text = labelOf(connection)
            setTextColor(TEXT)
            textSize = 13f
            maxLines = 1
            ellipsize = TextUtils.TruncateAt.END
        }, weightParams())
        bar.addView(Button(this).apply {
            text = "Hosts"
            isAllCaps = false
            textSize = 12f
            minWidth = 0
            minHeight = 0
            setPadding(dp(14), dp(4), dp(14), dp(4))
            setTextColor(TEXT)
            setBackgroundColor(CARD)
            setOnClickListener { showHosts(null) }
        }, LinearLayout.LayoutParams(
            ViewGroup.LayoutParams.WRAP_CONTENT,
            ViewGroup.LayoutParams.WRAP_CONTENT
        ))
        shell.addView(bar, LinearLayout.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT,
            dp(46)
        ))
        shell.addView(web, LinearLayout.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT,
            0,
            1f
        ))
        return shell
    }

    private fun showHosts(message: String?) {
        stopWeb()
        val registry = tokenStore.loadRegistry()
        if (registry.hosts.isEmpty()) {
            showPairing(message)
            return
        }
        val scroll = ScrollView(this).apply {
            setBackgroundColor(BACKGROUND)
            isFillViewport = true
        }
        val list = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(20), dp(36), dp(20), dp(24))
        }
        list.addView(TextView(this).apply {
            text = "Hosts"
            setTextColor(TEXT)
            textSize = 26f
        }, matchWrap())
        list.addView(TextView(this).apply {
            text = "One host is connected at a time. Its session and worktree ids stay on that host."
            setTextColor(MUTED)
            textSize = 12f
            setPadding(0, dp(6), 0, dp(14))
        }, matchWrap())
        if (!message.isNullOrEmpty()) {
            list.addView(TextView(this).apply {
                text = message
                setTextColor(ERROR)
                textSize = 13f
                setPadding(0, 0, 0, dp(14))
            }, matchWrap())
        }

        val ordered = registry.hosts.sortedWith(
            compareByDescending<TokenStore.Connection> { it.hostId == registry.activeHostId }
                .thenByDescending { it.lastSeenAt }
        )
        for (host in ordered) {
            list.addView(hostCard(host, host.hostId == registry.activeHostId), cardParams())
        }

        val add = Button(this).apply {
            text = "Add host"
            isAllCaps = false
            setTextColor(Color.WHITE)
            setBackgroundColor(ACCENT)
            setOnClickListener { showPairing(null) }
        }
        list.addView(add, fieldParams())
        list.addView(TextView(this).apply {
            text = "Scan a Pair phone QR with the camera, or enter the daemon URL and code. " +
                "Forgetting a host removes it from this phone only; revoke the device on the desktop too."
            setTextColor(MUTED)
            textSize = 12f
            setPadding(0, dp(12), 0, 0)
        }, matchWrap())

        scroll.addView(list, ViewGroup.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT,
            ViewGroup.LayoutParams.WRAP_CONTENT
        ))
        root.removeAllViews()
        root.addView(
            scroll,
            FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT
            )
        )
    }

    private fun hostCard(host: TokenStore.Connection, active: Boolean): LinearLayout {
        val card = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(16), dp(14), dp(16), dp(12))
            background = cardBackground()
        }
        val title = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
        }
        title.addView(TextView(this).apply {
            text = labelOf(host)
            setTextColor(TEXT)
            textSize = 17f
            maxLines = 1
            ellipsize = TextUtils.TruncateAt.END
        }, weightParams())
        if (active) {
            title.addView(TextView(this).apply {
                text = "LAST USED"
                setTextColor(ACCENT)
                textSize = 10f
                setPadding(dp(8), 0, 0, 0)
            }, LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT,
                ViewGroup.LayoutParams.WRAP_CONTENT
            ))
        }
        card.addView(title, matchWrap())

        card.addView(TextView(this).apply {
            text = host.base
            setTextColor(MUTED)
            textSize = 12f
            maxLines = 1
            ellipsize = TextUtils.TruncateAt.END
            setPadding(0, dp(3), 0, 0)
        }, matchWrap())

        val meta = buildString {
            append(scopeLabel(host.scope))
            append(" · ")
            append(if (host.isHybrid) "hybrid PQ" else "P-256")
            append(" · host ")
            append(host.hostId.take(8))
        }
        card.addView(TextView(this).apply {
            text = meta
            setTextColor(MUTED)
            textSize = 11f
            setPadding(0, dp(3), 0, dp(10))
        }, matchWrap())

        val actions = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
        }
        actions.addView(Button(this).apply {
            text = "Connect"
            isAllCaps = false
            setTextColor(Color.WHITE)
            setBackgroundColor(ACCENT)
            setOnClickListener { startWeb(host) }
        }, weightParams())
        actions.addView(Button(this).apply {
            text = "Forget"
            isAllCaps = false
            setTextColor(TEXT)
            setBackgroundColor(CARD)
            setOnClickListener { confirmForget(host) }
        }, LinearLayout.LayoutParams(
            ViewGroup.LayoutParams.WRAP_CONTENT,
            ViewGroup.LayoutParams.WRAP_CONTENT
        ).apply { leftMargin = dp(8) })
        card.addView(actions, matchWrap())
        return card
    }

    private fun confirmForget(host: TokenStore.Connection) {
        AlertDialog.Builder(this)
            .setTitle("Forget ${labelOf(host)}?")
            .setMessage(
                "This removes the token and pinned key from this phone. " +
                    "It does not revoke the device on the desktop."
            )
            .setNegativeButton("Cancel", null)
            .setPositiveButton("Forget") { _, _ ->
                val next = tokenStore.remove(host.hostId)
                if (next.hosts.isEmpty()) {
                    NotificationService.stop(this)
                    showPairing("Forgot ${labelOf(host)}. Pair a host to reconnect.")
                } else {
                    showHosts("Forgot ${labelOf(host)}. The desktop device row was not revoked.")
                }
            }
            .show()
    }

    private fun labelOf(connection: TokenStore.Connection): String {
        return connection.label.ifEmpty {
            NativePair.hostLabel(connection.base)
        }
    }

    private fun suiteLabel(connection: TokenStore.Connection): String {
        return if (connection.isHybrid) {
            "hybrid X25519 + ML-KEM-768"
        } else {
            "P-256 (legacy)"
        }
    }

    private fun scopeLabel(scope: String): String {
        return when (scope) {
            "full", "workspace" -> "Workspace access"
            "read" -> "Read-only"
            "" -> "Paired device"
            else -> "Scope unknown"
        }
    }

    private fun isLocalOrigin(uri: Uri?): Boolean {
        if (uri == null) return false
        return uri.scheme == "http" &&
            uri.host == originHost &&
            uri.port == proxyPort
    }

    private fun stopWeb() {
        connectGeneration++
        currentHostId = ""
        loopback?.stop()
        loopback = null
        val web = webView
        if (web != null) {
            root.removeView(web)
            web.destroy()
        }
        webView = null
        root.removeAllViews()
    }

    private fun dp(value: Int): Int {
        return Math.round(value * resources.displayMetrics.density)
    }

    private fun matchWrap(): LinearLayout.LayoutParams {
        return LinearLayout.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT,
            ViewGroup.LayoutParams.WRAP_CONTENT
        )
    }

    private fun weightParams(): LinearLayout.LayoutParams {
        return LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f)
    }

    private fun fieldParams(): LinearLayout.LayoutParams {
        return matchWrap().apply { topMargin = dp(10) }
    }

    private fun cardParams(): LinearLayout.LayoutParams {
        return matchWrap().apply { bottomMargin = dp(10) }
    }

    private fun cardBackground(): GradientDrawable {
        return GradientDrawable().apply {
            setColor(CARD)
            cornerRadius = dp(10).toFloat()
            setStroke(dp(1), BORDER)
        }
    }

    private companion object {
        val BACKGROUND = 0xFF080A0D.toInt()
        val CARD = 0xFF11161C.toInt()
        val BORDER = 0xFF222A33.toInt()
        val TEXT = 0xFFE6EDF3.toInt()
        val MUTED = 0xFF8B949E.toInt()
        val ERROR = 0xFFFF7B72.toInt()
        val ACCENT = 0xFF2F81F7.toInt()
        const val REQUEST_NOTIFICATIONS = 42
    }
}
