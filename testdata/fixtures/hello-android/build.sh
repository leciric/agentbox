#!/bin/sh
# Builds build/remedy.apk with the Android SDK's own tools, without Gradle, so
# it works offline in a few seconds. Needs ANDROID_HOME (with build-tools and
# a platform) and a JDK.
set -eu
cd "$(dirname "$0")"

sdk=${ANDROID_HOME:?set ANDROID_HOME to an Android SDK}
command -v javac >/dev/null || { echo "Install a JDK first: sudo apt-get install -y openjdk-21-jdk-headless" >&2; exit 1; }
tools=$(ls -d "$sdk"/build-tools/* | sort -V | tail -n 1)
platform=$(ls -d "$sdk"/platforms/android-* | sort -V | tail -n 1)
echo "build tools: $(basename "$tools"), platform: $(basename "$platform")"

rm -rf build
mkdir -p build/classes build/dex
"$tools/aapt2" link -I "$platform/android.jar" --manifest app/src/main/AndroidManifest.xml -o build/unsigned.apk
javac --release 11 -classpath "$platform/android.jar" -d build/classes $(find app/src/main/java -name '*.java')
"$tools/d8" --release --min-api 26 --lib "$platform/android.jar" --output build/dex $(find build/classes -name '*.class')
python3 -c 'import zipfile; z = zipfile.ZipFile("build/unsigned.apk", "a"); z.write("build/dex/classes.dex", "classes.dex"); z.close()'
"$tools/zipalign" -f 4 build/unsigned.apk build/aligned.apk
keytool -genkeypair -keystore build/debug.keystore -storepass android -keypass android -alias debug \
  -dname "CN=Remedy Debug" -keyalg RSA -validity 365 >/dev/null 2>&1
"$tools/apksigner" sign --ks build/debug.keystore --ks-pass pass:android --out build/remedy.apk build/aligned.apk
echo "built build/remedy.apk ($(stat -c %s build/remedy.apk) bytes)"
