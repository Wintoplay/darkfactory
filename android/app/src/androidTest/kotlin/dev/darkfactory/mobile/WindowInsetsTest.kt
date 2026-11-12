package dev.darkfactory.mobile

import android.app.Activity
import android.content.Intent
import android.graphics.Insets
import android.graphics.Rect
import android.os.Build
import android.test.InstrumentationTestCase
import android.view.DisplayCutout
import android.view.View
import android.view.ViewGroup
import android.view.WindowInsets
import android.webkit.WebView
import android.widget.FrameLayout

/** Run only on a disposable install: startup clears the saved host registry. */
@Suppress("DEPRECATION", "OVERRIDE_DEPRECATION")
class WindowInsetsTest : InstrumentationTestCase() {
    private lateinit var activity: Activity
    private lateinit var root: FrameLayout
    private var web: WebView? = null

    override fun setUp() {
        super.setUp()
        val context = instrumentation.targetContext
        TokenStore(context).clear()
        activity = instrumentation.startActivitySync(
            Intent(context, MainActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK)
        )
        instrumentation.waitForIdleSync()
        onMain {
            root = activity.findViewById<ViewGroup>(android.R.id.content).getChildAt(0) as FrameLayout
        }
    }

    override fun tearDown() {
        onMain {
            web?.destroy()
            activity.finish()
        }
        super.tearDown()
    }

    fun testPairingAndWebContentAvoidRealSystemBars() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.R) return
        onMain {
            assertInsideSystemBars(root.getChildAt(0))
            installWebContent()
        }
        instrumentation.waitForIdleSync()
        onMain { assertInsideSystemBars(web!!) }
    }

    fun testCutoutKeyboardAndRepeatedInsets() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.R) return
        onMain {
            installWebContent()
            val delivered = mutableListOf<WindowInsets>()
            web!!.setOnApplyWindowInsetsListener { _, insets ->
                delivered.add(insets)
                insets
            }
            val portrait = WindowInsets.Builder()
                .setInsets(WindowInsets.Type.systemBars(), Insets.of(0, 24, 0, 48))
                .setDisplayCutout(DisplayCutout(Insets.of(0, 54, 0, 0), null,
                    Rect(100, 0, 160, 54), null, null))
                .build()
            assertContentBounds(portrait, Rect(0, 54, root.width, root.height - 48))
            assertContentBounds(portrait, Rect(0, 54, root.width, root.height - 48))

            val keyboard = WindowInsets.Builder(portrait)
                .setInsets(WindowInsets.Type.ime(), Insets.of(0, 0, 0, 600))
                .setVisible(WindowInsets.Type.ime(), true)
                .build()
            assertContentBounds(keyboard, Rect(0, 54, root.width, root.height - 600))
            assertContentBounds(portrait, Rect(0, 54, root.width, root.height - 48))

            val landscape = WindowInsets.Builder()
                .setInsets(WindowInsets.Type.systemBars(), Insets.of(0, 24, 48, 0))
                .setDisplayCutout(DisplayCutout(Insets.of(96, 0, 0, 0),
                    Rect(0, 100, 96, 160), null, null, null))
                .build()
            assertContentBounds(landscape, Rect(96, 24, root.width - 48, root.height))
            assertTrue("Inset updates must reach the WebView", delivered.size >= 5)
            val handled = WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout() or
                WindowInsets.Type.ime()
            for (insets in delivered) {
                assertEquals("Native spacing must not be applied again by WebView",
                    Insets.NONE, insets.getInsets(handled))
            }
        }
    }

    private fun onMain(action: () -> Unit) {
        var failure: Throwable? = null
        instrumentation.runOnMainSync {
            try {
                action()
            } catch (error: Throwable) {
                failure = error
            }
        }
        failure?.let { throw it }
    }

    private fun installWebContent() {
        root.removeAllViews()
        web = WebView(activity)
        root.addView(web, FrameLayout.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT
        ))
        layoutRoot()
    }

    private fun assertInsideSystemBars(content: View) {
        val decor = activity.window.decorView
        val bars = decor.rootWindowInsets.getInsets(
            WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout()
        )
        val origin = IntArray(2)
        val position = IntArray(2)
        decor.getLocationOnScreen(origin)
        content.getLocationOnScreen(position)
        assertTrue("Content overlaps the left system bar or cutout", position[0] >= origin[0] + bars.left)
        assertTrue("Content overlaps the status bar or cutout", position[1] >= origin[1] + bars.top)
        assertTrue("Content overlaps the right system bar or cutout",
            position[0] + content.width <= origin[0] + decor.width - bars.right)
        assertTrue("Content overlaps the navigation bar",
            position[1] + content.height <= origin[1] + decor.height - bars.bottom)
    }

    private fun assertContentBounds(insets: WindowInsets, expected: Rect) {
        root.dispatchApplyWindowInsets(insets)
        layoutRoot()
        val content = web!!
        assertEquals(expected, Rect(content.left, content.top, content.right, content.bottom))
    }

    private fun layoutRoot() {
        root.measure(View.MeasureSpec.makeMeasureSpec(root.width, View.MeasureSpec.EXACTLY),
            View.MeasureSpec.makeMeasureSpec(root.height, View.MeasureSpec.EXACTLY))
        root.layout(root.left, root.top, root.right, root.bottom)
    }
}
