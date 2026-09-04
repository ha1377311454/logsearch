package io.github.ha1377311454.logsearch.settings

import com.intellij.credentialStore.CredentialAttributes
import com.intellij.credentialStore.generateServiceName
import com.intellij.ide.passwordSafe.PasswordSafe

object TokenStore {
    private fun attributes(environmentId: String) = CredentialAttributes(
        generateServiceName("LogSearch", environmentId),
    )

    fun get(environmentId: String): String =
        PasswordSafe.instance.getPassword(attributes(environmentId)).orEmpty()

    fun set(environmentId: String, token: String) {
        PasswordSafe.instance.setPassword(attributes(environmentId), token.takeIf { it.isNotBlank() })
    }

    fun remove(environmentId: String) {
        PasswordSafe.instance.setPassword(attributes(environmentId), null)
    }
}
