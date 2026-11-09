package dev.darkfactory.mobile

import android.app.Activity
import android.content.Intent
import android.test.InstrumentationTestCase

/** Launch the real activity with unusable saved keys and verify the pin survives. */
@Suppress("DEPRECATION")
class PinRetentionTest : InstrumentationTestCase() {
    private var activity: Activity? = null

    override fun tearDown() {
        activity?.let { current -> instrumentation.runOnMainSync { current.finish() } }
        TokenStore(instrumentation.targetContext).clear()
        super.tearDown()
    }

    fun testMissingHybridBundleKeepsSavedPin() {
        assertStartupKeepsPin(HostKey.SUITE)
    }

    fun testMissingLegacyClientKeyKeepsSavedPin() {
        assertStartupKeepsPin("")
    }

    private fun assertStartupKeepsPin(suite: String) {
        val context = instrumentation.targetContext
        val store = TokenStore(context)
        // The runner is used only on a disposable test install.
        store.clear()
        val hostId = "pin-retention-fixture"
        val pin = "saved-pin-must-survive"
        store.save(TokenStore.Connection(
            base = "http://127.0.0.1:1",
            hostId = hostId,
            token = "fixture-token",
            deviceId = "fixture-device",
            hostPublicKey = "fixture-host-key",
            fingerprint = pin,
            suite = suite,
            hostBundle = "",
            fingerprintV2 = if (suite == HostKey.SUITE) pin else ""
        ))
        val intent = Intent(context, MainActivity::class.java)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK)
        activity = instrumentation.startActivitySync(intent)
        instrumentation.waitForIdleSync()
        val saved = store.loadRegistry().hosts
        assertEquals("Startup must retain the unusable host", 1, saved.size)
        assertEquals(hostId, saved[0].hostId)
        assertEquals("Startup must preserve the exact pin", pin, saved[0].fingerprint)
    }
}
