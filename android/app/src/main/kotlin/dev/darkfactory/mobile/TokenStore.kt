package dev.darkfactory.mobile

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import org.json.JSONArray
import org.json.JSONObject
import java.nio.charset.StandardCharsets
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Stores the per-host device state inside Android Keystore-backed encryption.
 * The web page never sees a token or a client private key: the loopback proxy
 * owns them. Each host has its own token, client key, pinned daemon key, and
 * host id; switching a host tears down the previous loopback origin and WebView.
 */
internal class TokenStore(context: Context) {
    data class Connection(
        val base: String,
        val hostId: String,
        val token: String,
        val deviceId: String = "",
        val hostPublicKey: String = "",
        val clientPrivateKey: String = "",
        val clientPublicKey: String = "",
        val fingerprint: String = "",
        val label: String = "",
        val scope: String = "",
        val lastSeenAt: Long = 0L
    ) {
        val hasE2EE: Boolean
            get() = deviceId.isNotEmpty() &&
                hostPublicKey.isNotEmpty() &&
                clientPrivateKey.isNotEmpty() &&
                clientPublicKey.isNotEmpty()
    }

    data class Registry(
        val hosts: List<Connection> = emptyList(),
        val activeHostId: String? = null
    ) {
        val active: Connection?
            get() = hosts.firstOrNull { it.hostId == activeHostId } ?: hosts.firstOrNull()
    }

    private val preferences = context.applicationContext
        .getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)

    @Synchronized
    fun loadRegistry(): Registry = loadRegistryUnlocked()

    @Synchronized
    fun load(): Connection? = loadRegistryUnlocked().active

    @Synchronized
    fun save(connection: Connection) {
        val current = loadRegistryUnlocked()
        val now = System.currentTimeMillis()
        val stored = connection.copy(
            label = connection.label.ifEmpty { defaultLabel(connection.base) },
            lastSeenAt = now
        )
        val hosts = current.hosts.toMutableList()
        val index = hosts.indexOfFirst { it.hostId == stored.hostId }
        if (index >= 0) {
            hosts[index] = stored
        } else {
            if (hosts.size >= MAX_HOSTS) {
                throw IllegalStateException(
                    "This phone already stores $MAX_HOSTS hosts. Forget one before pairing another."
                )
            }
            hosts.add(stored)
        }
        writeRegistry(Registry(hosts, stored.hostId))
    }

    @Synchronized
    fun setActive(hostId: String) {
        val current = loadRegistryUnlocked()
        if (current.hosts.none { it.hostId == hostId }) return
        writeRegistry(current.copy(activeHostId = hostId))
    }

    @Synchronized
    fun markSeen(hostId: String) {
        val current = loadRegistryUnlocked()
        val now = System.currentTimeMillis()
        var changed = false
        val hosts = current.hosts.map { host ->
            if (host.hostId == hostId && now - host.lastSeenAt > SEEN_INTERVAL_MS) {
                changed = true
                host.copy(lastSeenAt = now)
            } else {
                host
            }
        }
        if (changed) writeRegistry(current.copy(hosts = hosts, activeHostId = hostId))
    }

    @Synchronized
    fun remove(hostId: String): Registry {
        val current = loadRegistryUnlocked()
        val hosts = current.hosts.filterNot { it.hostId == hostId }
        if (hosts.isEmpty()) {
            clear()
            return Registry()
        }
        val active = if (current.activeHostId == hostId) {
            hosts.first().hostId
        } else {
            current.activeHostId
        }
        val next = Registry(hosts, active)
        writeRegistry(next)
        return next
    }

    @Synchronized
    fun clear() {
        preferences.edit().clear().apply()
        try {
            val keyStore = KeyStore.getInstance(ANDROID_KEYSTORE)
            keyStore.load(null)
            if (keyStore.containsAlias(KEY_ALIAS)) keyStore.deleteEntry(KEY_ALIAS)
        } catch (_: Exception) {
            // The preference payload is already gone; a stale key is harmless.
        }
    }

    private fun loadRegistryUnlocked(): Registry {
        val state = preferences.getString("state", "").orEmpty()
        val iv = preferences.getString("iv", "").orEmpty()
        if (state.isNotEmpty() && iv.isNotEmpty()) {
            return try {
                parseRegistry(JSONObject(decrypt(state, iv)))
            } catch (_: Exception) {
                clear()
                Registry()
            }
        }

        // Rows written by the first Java/Kotlin shell stored three plaintext
        // fields separately. Read them once so a paired phone survives the
        // upgrade; the next save writes the encrypted registry.
        val encoded = preferences.getString("token", "").orEmpty()
        val legacyIv = preferences.getString("iv", "").orEmpty()
        if (encoded.isEmpty() || legacyIv.isEmpty()) return Registry()
        val base = preferences.getString("base", "").orEmpty()
        val hostId = preferences.getString("hostId", "").orEmpty()
        if (base.isEmpty() || hostId.isEmpty()) {
            clear()
            return Registry()
        }
        return try {
            val token = decrypt(encoded, legacyIv)
            Registry(
                hosts = listOf(Connection(base, hostId, token, label = defaultLabel(base))),
                activeHostId = hostId
            )
        } catch (_: Exception) {
            clear()
            Registry()
        }
    }

    private fun parseRegistry(json: JSONObject): Registry {
        val hosts = mutableListOf<Connection>()
        val array = json.optJSONArray("hosts")
        if (array != null) {
            for (index in 0 until array.length()) {
                val item = array.optJSONObject(index) ?: continue
                try {
                    hosts.add(connectionFromJson(item))
                } catch (_: Exception) {
                    // Drop one unreadable host without discarding the others.
                }
            }
        } else if (json.has("base")) {
            // The first encrypted build stored one connection object.
            hosts.add(connectionFromJson(json))
        }
        if (hosts.isEmpty()) return Registry()
        val requested = json.optString("activeHostId", "")
        val active = hosts.firstOrNull { it.hostId == requested }?.hostId ?: hosts.first().hostId
        return Registry(hosts, active)
    }

    private fun writeRegistry(registry: Registry) {
        val hosts = JSONArray()
        for (host in registry.hosts) hosts.put(connectionToJson(host))
        val plain = JSONObject()
            .put("v", 2)
            .put("activeHostId", registry.activeHostId.orEmpty())
            .put("hosts", hosts)
            .toString()
        val (ciphertext, iv) = encrypt(plain)
        preferences.edit()
            .putString("state", ciphertext)
            .putString("iv", iv)
            .remove("token")
            .remove("base")
            .remove("hostId")
            .apply()
    }

    private fun connectionToJson(connection: Connection): JSONObject {
        return JSONObject()
            .put("base", connection.base)
            .put("hostId", connection.hostId)
            .put("token", connection.token)
            .put("deviceId", connection.deviceId)
            .put("hostPublicKey", connection.hostPublicKey)
            .put("clientPrivateKey", connection.clientPrivateKey)
            .put("clientPublicKey", connection.clientPublicKey)
            .put("fingerprint", connection.fingerprint)
            .put("label", connection.label)
            .put("scope", connection.scope)
            .put("lastSeenAt", connection.lastSeenAt)
    }

    private fun connectionFromJson(json: JSONObject): Connection {
        val base = json.optString("base", "")
        val hostId = json.optString("hostId", "")
        val token = json.optString("token", "")
        if (base.isEmpty() || hostId.isEmpty() || token.isEmpty()) {
            throw IllegalStateException("stored connection is incomplete")
        }
        return Connection(
            base = base,
            hostId = hostId,
            token = token,
            deviceId = json.optString("deviceId", ""),
            hostPublicKey = json.optString("hostPublicKey", ""),
            clientPrivateKey = json.optString("clientPrivateKey", ""),
            clientPublicKey = json.optString("clientPublicKey", ""),
            fingerprint = json.optString("fingerprint", ""),
            label = json.optString("label", "").ifEmpty { defaultLabel(base) },
            scope = json.optString("scope", ""),
            lastSeenAt = json.optLong("lastSeenAt", 0L)
        )
    }

    private fun defaultLabel(base: String): String {
        val authority = base.substringAfter("://", base).substringBefore('/')
        return authority.ifEmpty { base }
    }

    private fun encrypt(plain: String): Pair<String, String> {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key())
        val encrypted = cipher.doFinal(plain.toByteArray(StandardCharsets.UTF_8))
        return Pair(
            Base64.encodeToString(encrypted, Base64.NO_WRAP),
            Base64.encodeToString(cipher.iv, Base64.NO_WRAP)
        )
    }

    private fun decrypt(encoded: String, iv: String): String {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(
            Cipher.DECRYPT_MODE,
            key(),
            GCMParameterSpec(GCM_TAG_BITS, Base64.decode(iv, Base64.NO_WRAP))
        )
        val plain = cipher.doFinal(Base64.decode(encoded, Base64.NO_WRAP))
        return String(plain, StandardCharsets.UTF_8)
    }

    private fun key(): SecretKey {
        val keyStore = KeyStore.getInstance(ANDROID_KEYSTORE)
        keyStore.load(null)
        if (keyStore.containsAlias(KEY_ALIAS)) {
            return keyStore.getKey(KEY_ALIAS, null) as SecretKey
        }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, ANDROID_KEYSTORE)
        generator.init(
            KeyGenParameterSpec.Builder(
                KEY_ALIAS,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT
            )
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setUserAuthenticationRequired(false)
                .build()
        )
        return generator.generateKey()
    }

    private companion object {
        const val PREFERENCES = "darkfactory-native"
        const val KEY_ALIAS = "darkfactory-device-token"
        const val ANDROID_KEYSTORE = "AndroidKeyStore"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val GCM_TAG_BITS = 128
        const val MAX_HOSTS = 16
        const val SEEN_INTERVAL_MS = 60_000L
    }
}
