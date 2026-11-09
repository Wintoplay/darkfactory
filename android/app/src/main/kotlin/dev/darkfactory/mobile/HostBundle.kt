package dev.darkfactory.mobile

import org.json.JSONObject

/**
 * Parses and verifies the daemon's signed key bundle.
 *
 * The JSON stays here, in Kotlin; the signature, the canonical encoding and the
 * v2 pin live in [HostKey], which is plain Java so the JVM interop harness runs
 * the same code. A bundle is accepted only if its signature verifies under the
 * P-256 key it carries, and only if it names the suite this app implements.
 */
internal object HostBundle {
    const val VERSION = 2

    /** Parses a bundle object from the host-key or pair response. */
    fun parse(bundle: JSONObject): HostKey {
        val version = requiredInteger(bundle, "v")
        HostKey.requireBundleVersion(version)
        return HostKey.verify(
            requiredString(bundle, "p256"),
            requiredInteger(bundle, "epoch"),
            requiredString(bundle, "suite"),
            requiredString(bundle, "x25519"),
            requiredString(bundle, "mlkem"),
            requiredString(bundle, "sig")
        )
    }

    private fun requiredString(bundle: JSONObject, name: String): String =
        bundle.opt(name) as? String
            ?: throw IllegalStateException("The host key bundle field '$name' is invalid.")

    private fun requiredInteger(bundle: JSONObject, name: String): Int {
        val value = bundle.opt(name) as? Number
            ?: throw IllegalStateException("The host key bundle field '$name' is invalid.")
        val longValue = value.toLong()
        if (value.toDouble() != longValue.toDouble() ||
            longValue < Int.MIN_VALUE.toLong() || longValue > Int.MAX_VALUE.toLong()
        ) {
            throw IllegalStateException("The host key bundle field '$name' is invalid.")
        }
        return longValue.toInt()
    }

    /** The stored form is the bundle JSON exactly as the daemon sent it. */
    fun stored(json: String): HostKey {
        if (json.isEmpty()) {
            throw IllegalStateException("This host has no stored key bundle. Pair again.")
        }
        val bundle = try {
            JSONObject(json)
        } catch (error: Exception) {
            throw IllegalStateException("The stored host bundle is unreadable. Pair again.", error)
        }
        return parse(bundle)
    }
}
