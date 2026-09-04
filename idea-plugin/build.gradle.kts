import org.gradle.api.artifacts.repositories.MavenArtifactRepository

plugins {
    id("java")
    id("org.jetbrains.kotlin.jvm") version "2.1.20"
    id("org.jetbrains.intellij.platform") version "2.18.1"
}

group = "io.github.ha1377311454.logsearch"
version = "0.1.0"

repositories {
    // 兼容本机 ~/.gradle/init.gradle 注入的旧 HTTP Maven 镜像。
    // 插件依赖均来自 Maven Central 和 JetBrains 官方仓库，不使用已废弃的阿里云 JCenter 端点。
    removeIf { repository ->
        repository is MavenArtifactRepository && repository.url.host.equals("maven.aliyun.com", ignoreCase = true)
    }
    mavenCentral()
    intellijPlatform { defaultRepositories() }
}

dependencies {
    implementation("com.google.code.gson:gson:2.11.0")
    intellijPlatform {
        intellijIdeaCommunity("2024.3.7")
    }
}

kotlin {
    jvmToolchain(17)
}

intellijPlatform {
    pluginConfiguration {
        id = "io.github.ha1377311454.logsearch"
        name = "LogSearch"
        version = project.version.toString()
        description = "Search distributed Kubernetes node logs from IntelliJ IDEA."
        vendor {
            name = "Helianthus"
        }
        ideaVersion {
            sinceBuild = "243"
        }
    }
}
