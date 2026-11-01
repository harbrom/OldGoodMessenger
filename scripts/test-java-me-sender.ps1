$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$tools = Join-Path $root '.tools/java-me'
$jdk = Join-Path $tools 'jdk/jdk8u504-b01/bin'
$apis = (Join-Path $tools 'cldcapi11-2.0.4.jar') + ';' + (Join-Path $tools 'midpapi20-2.0.4.jar')
$out = Join-Path $root ('.build/java-me-sender/' + [Guid]::NewGuid().ToString('N'))
$classes = Join-Path $out 'classes'
New-Item -ItemType Directory -Force -Path $classes | Out-Null
$sources = @((Join-Path $root 'clients/java-me/src/ogm/Utf8.java'))
$sources += @(Get-ChildItem (Join-Path $root 'clients/java-me/delivery-v1/ogm/delivery/v1') -Filter '*.java' | ForEach-Object { $_.FullName })
$sources += Join-Path $root 'clients/java-me/test/ogm/delivery/v1/SenderTest.java'
$sources += Join-Path $root 'clients/java-me/test/ogm/delivery/v1/SenderEmulatorTest.java'
& (Join-Path $jdk 'javac.exe') -encoding UTF-8 -source 1.3 -target 1.1 -bootclasspath $apis -d $classes @sources
if ($LASTEXITCODE -ne 0) { throw 'Sender CLDC/MIDP compilation failed.' }
$raw = Join-Path $out 'raw.jar'
$verified = Join-Path $out 'verified.jar'
& (Join-Path $jdk 'jar.exe') cMf $raw -C $classes .
if ($LASTEXITCODE -ne 0) { throw 'Sender intermediate packaging failed.' }
& (Join-Path $jdk 'java.exe') -jar (Join-Path $tools 'proguard-base-6.2.2.jar') -injars $raw -outjars $verified -libraryjars $apis -microedition -target 1.1 -dontshrink -dontoptimize -dontobfuscate -keep 'class ** { *; }'
if ($LASTEXITCODE -ne 0) { throw 'Sender CLDC preverification failed.' }
$entries = @(& (Join-Path $jdk 'jar.exe') tf $verified)
if ($LASTEXITCODE -ne 0) { throw 'Cannot inspect sender classes.' }
foreach ($entry in $entries) {
    if (-not $entry.EndsWith('.class')) { continue }
    $name = $entry.Substring(0, $entry.Length - 6).Replace('/', '.')
    $details = & (Join-Path $jdk 'javap.exe') -v -private -classpath $verified $name
    if ($LASTEXITCODE -ne 0 -or -not ($details -match 'major version: 45') -or -not ($details -match 'minor version: 3')) { throw "Unexpected class version: $name" }
    if ($name -in @('ogm.delivery.v1.Sender', 'ogm.delivery.v1.Sender$Link', 'ogm.delivery.v1.SenderTest', 'ogm.delivery.v1.SenderEmulatorTest') -and -not ($details -match 'StackMap:')) { throw "Missing CLDC StackMap: $name" }
}
& (Join-Path $jdk 'java.exe') -cp ($verified + ';' + (Join-Path $tools 'microemulator-2.0.4.jar')) ogm.delivery.v1.SenderTest
if ($LASTEXITCODE -ne 0) { throw 'Focused sender tests failed.' }
$metadata = @"
Manifest-Version: 1.0
MIDlet-Name: OGM Sender Test
MIDlet-Version: 0.0.1
MIDlet-Vendor: OGM
MIDlet-1: OGM Sender Test,,ogm.delivery.v1.SenderEmulatorTest
MicroEdition-Configuration: CLDC-1.1
MicroEdition-Profile: MIDP-2.0
MIDlet-Permissions: javax.microedition.io.Connector.socket

"@
$manifest = Join-Path $out 'MANIFEST.MF'
[IO.File]::WriteAllText($manifest, $metadata.Replace("`r`n", "`n").Replace("`n", "`r`n"), [Text.Encoding]::ASCII)
$jar = Join-Path $out 'ogm-sender-test.jar'
Copy-Item -LiteralPath $verified -Destination $jar
& (Join-Path $jdk 'jar.exe') ufm $jar $manifest
if ($LASTEXITCODE -ne 0) { throw 'Sender test MIDlet packaging failed.' }
$previousJar = $env:OGM_JAVA_ME_SENDER_JAR
try {
    $env:OGM_JAVA_ME_SENDER_JAR = $jar
    & powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $root 'scripts/go.ps1') test -count=1 -timeout=150s -run '^TestJavaMESender$' -v ./internal/deliveryloopback
    if ($LASTEXITCODE -ne 0) { throw 'Java ME / Go loopback integration failed.' }
} finally { $env:OGM_JAVA_ME_SENDER_JAR = $previousJar }
Write-Output "PASS: sender CLDC/MIDP compile/preverification, focused tests, and real TCP/SQLite/file-RMS integration. Output: $out"
