#!/usr/bin/env bash
# Builds a signed release APK of the phone app locally (no EAS, no Metro needed:
# the JS bundle is embedded). Output: mobile/android/app/build/outputs/apk/release/app-release.apk
#
# Needs a JDK 17+ (JAVA_HOME or `java` on PATH) and the Android SDK (ANDROID_HOME,
# default ~/android-sdk; missing parts are downloaded by Gradle once the licenses are accepted).
# Signing: $AGENTBOX_MOBILE_KEYSTORE, default ~/.config/agentbox-mobile/mobile-release.jks (password in mobile-release.pass beside it),
# generated on first run and kept outside git.
# Keep it: only an APK signed with the same key installs over an earlier one.
set -euo pipefail
cd "$(dirname "$0")/.."

export ANDROID_HOME="${ANDROID_HOME:-$HOME/android-sdk}"
KEYSTORE="${AGENTBOX_MOBILE_KEYSTORE:-$HOME/.config/agentbox-mobile/mobile-release.jks}"
PASSFILE="${KEYSTORE%.jks}.pass"

if [ ! -d "$ANDROID_HOME/licenses" ]; then
  sdkmanager="$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager"
  [ -x "$sdkmanager" ] || { echo "Android SDK not found at $ANDROID_HOME (set ANDROID_HOME)" >&2; exit 1; }
  yes | "$sdkmanager" --licenses >/dev/null || true
fi

if [ ! -f "$KEYSTORE" ]; then
  mkdir -p "$(dirname "$KEYSTORE")"
  umask 077
  openssl rand -hex 16 > "$PASSFILE"
  keytool -genkeypair -v -keystore "$KEYSTORE" -alias agentbox -keyalg RSA -keysize 2048 \
    -validity 10000 -dname "CN=AgentBox, O=AgentBox" \
    -storepass "$(cat "$PASSFILE")" -keypass "$(cat "$PASSFILE")" >/dev/null
  echo "generated $KEYSTORE (password in $PASSFILE): back both up"
fi

npm ci
# prebuild rewrites the android/ios scripts in package.json; restore it afterwards.
cp package.json /tmp/agentbox-mobile-package.json.$$
npx expo prebuild --platform android --clean --no-install
cp /tmp/agentbox-mobile-package.json.$$ package.json && rm /tmp/agentbox-mobile-package.json.$$

# Sign the release build with our keystore instead of the debug one prebuild generates.
init="$(mktemp --suffix=.gradle)"
trap 'rm -f "$init"' EXIT
cat > "$init" <<GRADLE
allprojects {
  afterEvaluate { p ->
    if (p.plugins.hasPlugin('com.android.application')) {
      def pass = new File('$PASSFILE').text.trim()
      p.android.signingConfigs {
        release {
          storeFile new File('$KEYSTORE')
          storePassword pass
          keyAlias 'agentbox'
          keyPassword pass
        }
      }
      p.android.buildTypes.release.signingConfig = p.android.signingConfigs.release
    }
  }
}
GRADLE
(cd android && ./gradlew --init-script "$init" assembleRelease -PreactNativeArchitectures=arm64-v8a,x86_64)

apk="$PWD/android/app/build/outputs/apk/release/app-release.apk"
ls -l "$apk"
echo "$apk"
