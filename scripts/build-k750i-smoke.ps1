$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$tools = Join-Path $root '.tools/java-me'
$jdk = Join-Path $tools 'jdk/jdk8u504-b01/bin'
$apis = (Join-Path $tools 'cldcapi11-2.0.4.jar') + ';' + (Join-Path $tools 'midpapi20-2.0.4.jar')
$out = Join-Path $root '.build/java-me/ogm-storage-smoke'
$work = Join-Path $out ([Guid]::NewGuid().ToString('N'))
$classes = Join-Path $work 'classes'
New-Item -ItemType Directory -Force -Path $classes | Out-Null
$sources = @('clients/java-me/src/ogm/Utf8.java', 'clients/java-me/smoke/ogm/delivery/smoke/StorageSmoke.java')
# HistoryReader supplies transitive Page/Consumer type signatures at compilation.
# Shrinking removes its unused networking and HistoryStore's unused replay path.
foreach ($name in @('StateStorage', 'Codec', 'WriterState', 'RmsStateStorage', 'HistoryStore', 'RmsHistoryStorage', 'HistoryReader')) {
    $sources += "clients/java-me/delivery-v1/ogm/delivery/v1/$name.java"
}
$sources = @($sources | ForEach-Object { Join-Path $root $_ })
& (Join-Path $jdk 'javac.exe') -encoding UTF-8 -source 1.3 -target 1.1 -bootclasspath $apis -d $classes @sources
if ($LASTEXITCODE -ne 0) { throw 'Storage smoke CLDC/MIDP compilation failed.' }
$raw = Join-Path $work 'raw.jar'
$verified = Join-Path $work 'verified.jar'
& (Join-Path $jdk 'jar.exe') cMf $raw -C $classes .
if ($LASTEXITCODE -ne 0) { throw 'Storage smoke intermediate packaging failed.' }
& (Join-Path $jdk 'java.exe') -jar (Join-Path $tools 'proguard-base-6.2.2.jar') -injars $raw -outjars $verified -libraryjars $apis -microedition -target 1.1 -dontoptimize -dontobfuscate '-keep,includedescriptorclasses' 'class ogm.delivery.smoke.** { *; }'
if ($LASTEXITCODE -ne 0) { throw 'Storage smoke shrinking/preverification failed.' }
$entries = @(& (Join-Path $jdk 'jar.exe') tf $verified)
if ($LASTEXITCODE -ne 0) { throw 'Cannot inspect smoke package.' }
foreach ($entry in $entries) {
    if (-not $entry.EndsWith('.class')) { throw "Unexpected packaged resource: $entry" }
    if ($entry -notmatch '^ogm/(Utf8|delivery/(smoke/StorageSmoke(\$[0-9]+)?|v1/(Codec(\$Frame)?|StateStorage|WriterState|RmsStateStorage|HistoryStore|RmsHistoryStorage)))\.class$') { throw "Unexpected packaged class: $entry" }
    $name = $entry.Substring(0, $entry.Length - 6).Replace('/', '.')
    $details = & (Join-Path $jdk 'javap.exe') -v -private -classpath $verified $name
    if ($LASTEXITCODE -ne 0 -or -not ($details -match 'major version: 45') -or -not ($details -match 'minor version: 3')) { throw "Unexpected class version: $name" }
    if ($details -match 'javax/microedition/io/|org/microemu/|ogm/FirstContact|ogm/delivery/v1/(HistoryReader|Sender)') { throw "Unexpected runtime dependency: $name" }
    if ($name -in @('ogm.delivery.smoke.StorageSmoke', 'ogm.delivery.v1.WriterState', 'ogm.delivery.v1.HistoryStore', 'ogm.delivery.v1.RmsStateStorage', 'ogm.delivery.v1.RmsHistoryStorage') -and -not ($details -match 'StackMap:')) { throw "Missing CLDC StackMap: $name" }
}
$metadata = @"
Manifest-Version: 1.0
MIDlet-Name: OGM Storage Smoke
MIDlet-Version: 0.0.1
MIDlet-Vendor: OGM
MIDlet-1: OGM Storage Smoke,,ogm.delivery.smoke.StorageSmoke
MicroEdition-Configuration: CLDC-1.1
MicroEdition-Profile: MIDP-2.0

"@
$manifest = Join-Path $work 'MANIFEST.MF'
[IO.File]::WriteAllText($manifest, $metadata.Replace("`r`n", "`n").Replace("`n", "`r`n"), [Text.Encoding]::ASCII)
$jar = Join-Path $out 'ogm-storage-smoke.jar'
$jad = Join-Path $out 'ogm-storage-smoke.jad'
Copy-Item -LiteralPath $verified -Destination $jar -Force
& (Join-Path $jdk 'jar.exe') ufm $jar $manifest
if ($LASTEXITCODE -ne 0) { throw 'Storage smoke final packaging failed.' }
$properties = $metadata.TrimEnd() + "`nMIDlet-Jar-URL: ogm-storage-smoke.jar`nMIDlet-Jar-Size: $((Get-Item -LiteralPath $jar).Length)`n"
[IO.File]::WriteAllText($jad, $properties.Replace("`r`n", "`n").Replace("`n", "`r`n"), [Text.Encoding]::ASCII)
Add-Type -AssemblyName System.IO.Compression.FileSystem
$zip = [IO.Compression.ZipFile]::OpenRead($jar)
try {
    $reader = New-Object IO.StreamReader($zip.GetEntry('META-INF/MANIFEST.MF').Open())
    try { $actual = $reader.ReadToEnd() -replace "\r?\n ", '' } finally { $reader.Dispose() }
    foreach ($line in ($metadata.TrimEnd() -split '\r?\n')) {
        if (($actual -split '\r?\n') -notcontains $line) { throw "Missing manifest attribute: $line" }
    }
    if ($actual -match 'MIDlet-Permissions|MicroEmulator') { throw 'Unexpected permission or emulator requirement.' }
} finally { $zip.Dispose() }
Write-Output "PASS: offline package, CLDC 45.3/StackMaps, standalone dependencies, manifest/JAD metadata."
Get-Item -LiteralPath $jar, $jad | Select-Object FullName, Length
