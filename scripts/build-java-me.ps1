param([switch]$Test)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$tools = Join-Path $root '.tools/java-me'
$jdk = Join-Path $tools 'jdk/jdk8u504-b01/bin'
$name = 'ogm-first-contact'
if ($Test) { $name = 'ogm-emulator-test' }
$out = Join-Path $root ".build/java-me/$name"
$classes = Join-Path $out 'classes'
# A fresh unique compilation directory prevents stale classes entering an artifact.
$classes = Join-Path $classes ([Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $classes | Out-Null
$apis = (Join-Path $tools 'cldcapi11-2.0.4.jar') + ';' + (Join-Path $tools 'midpapi20-2.0.4.jar')
$sources = @(Get-ChildItem (Join-Path $root 'clients/java-me/src') -Recurse -Filter '*.java' | ForEach-Object { $_.FullName })
if ($Test) { $sources += Join-Path $root 'clients/java-me/test/ogm/EmulatorTest.java' }
& (Join-Path $jdk 'javac.exe') -encoding UTF-8 -source 1.3 -target 1.1 -bootclasspath $apis -d $classes @sources
if ($LASTEXITCODE -ne 0) { throw 'Java ME compilation failed.' }
$raw = Join-Path $out 'raw.jar'
& (Join-Path $jdk 'jar.exe') cMf $raw -C $classes .
if ($LASTEXITCODE -ne 0) { throw 'Intermediate packaging failed.' }
$verified = Join-Path $out 'verified.jar'
& (Join-Path $jdk 'java.exe') -jar (Join-Path $tools 'proguard-base-6.2.2.jar') -injars $raw -outjars $verified -libraryjars $apis -microedition -target 1.1 -dontshrink -dontoptimize -dontobfuscate -keep 'class ogm.** { *; }'
if ($LASTEXITCODE -ne 0) { throw 'CLDC preverification failed.' }
$main = 'ogm.FirstContact'
if ($Test) { $main = 'ogm.EmulatorTest' }
$metadata = @"
Manifest-Version: 1.0
MIDlet-Name: OGM First Contact
MIDlet-Version: 0.0.1
MIDlet-Vendor: OGM
MIDlet-1: OGM First Contact,,$main
MicroEdition-Configuration: CLDC-1.1
MicroEdition-Profile: MIDP-2.0
MIDlet-Permissions: javax.microedition.io.Connector.socket

"@
$manifest = Join-Path $out 'MANIFEST.MF'
[IO.File]::WriteAllText($manifest, $metadata.Replace("`r`n", "`n").Replace("`n", "`r`n"), [Text.Encoding]::ASCII)
$jar = Join-Path $out "$name.jar"
Copy-Item -LiteralPath $verified -Destination $jar -Force
& (Join-Path $jdk 'jar.exe') ufm $jar $manifest
if ($LASTEXITCODE -ne 0) { throw 'Final packaging failed.' }
$jad = $metadata.TrimEnd() + "`nMIDlet-Jar-URL: $name.jar`nMIDlet-Jar-Size: $((Get-Item -LiteralPath $jar).Length)`nOGM-Server: 127.0.0.1:19090`n"
[IO.File]::WriteAllText((Join-Path $out "$name.jad"), $jad.Replace("`r`n", "`n").Replace("`n", "`r`n"), [Text.Encoding]::ASCII)
Get-Item -LiteralPath $jar | Select-Object FullName, Length
