$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$tools = Join-Path $root '.tools/java-me'
$jdk = Join-Path $tools 'jdk/jdk8u504-b01/bin'
$apis = (Join-Path $tools 'cldcapi11-2.0.4.jar') + ';' + (Join-Path $tools 'midpapi20-2.0.4.jar')
$out = Join-Path $root ('.build/java-me-outbox/' + [Guid]::NewGuid().ToString('N'))
$classes = Join-Path $out 'classes'
$emulatorHome = Join-Path $out 'emulator-home'
New-Item -ItemType Directory -Force -Path $classes, $emulatorHome | Out-Null
$sources = @((Join-Path $root 'clients/java-me/src/ogm/Utf8.java'))
$sources += @(Get-ChildItem (Join-Path $root 'clients/java-me/delivery-v1/ogm/delivery/v1') -Filter '*.java' | ForEach-Object { $_.FullName })
$sources += Join-Path $root 'clients/java-me/test/WriterStateTest.java'
$sources += Join-Path $root 'clients/java-me/test/ogm/delivery/v1/RmsOutboxTest.java'
& (Join-Path $jdk 'javac.exe') -encoding UTF-8 -source 1.3 -target 1.1 -bootclasspath $apis -d $classes @sources
if ($LASTEXITCODE -ne 0) { throw 'Outbox CLDC/MIDP-only compilation failed.' }
$raw = Join-Path $out 'raw.jar'
$verified = Join-Path $out 'verified.jar'
& (Join-Path $jdk 'jar.exe') cMf $raw -C $classes .
if ($LASTEXITCODE -ne 0) { throw 'Outbox intermediate packaging failed.' }
& (Join-Path $jdk 'java.exe') -jar (Join-Path $tools 'proguard-base-6.2.2.jar') -injars $raw -outjars $verified -libraryjars $apis -microedition -target 1.1 -dontshrink -dontoptimize -dontobfuscate -keep 'class ** { *; }'
if ($LASTEXITCODE -ne 0) { throw 'Outbox preverification failed.' }
$names = @(& (Join-Path $jdk 'jar.exe') tf $verified)
if ($LASTEXITCODE -ne 0) { throw 'Cannot inspect outbox classes.' }
foreach ($entry in $names) {
    if (-not $entry.EndsWith('.class')) { continue }
    $name = $entry.Substring(0, $entry.Length - 6).Replace('/', '.')
    $details = & (Join-Path $jdk 'javap.exe') -v -private -classpath $verified $name
    if ($LASTEXITCODE -ne 0 -or -not ($details -match 'major version: 45') -or -not ($details -match 'minor version: 3')) { throw "Unexpected class version: $name" }
    if ($name -in @('ogm.delivery.v1.WriterState', 'ogm.delivery.v1.RmsStateStorage', 'WriterStateTest', 'ogm.delivery.v1.RmsOutboxTest') -and -not ($details -match 'StackMap:')) { throw "Missing CLDC StackMap: $name" }
}
& (Join-Path $jdk 'java.exe') -cp $verified WriterStateTest
if ($LASTEXITCODE -ne 0) { throw 'Outbox state-machine tests failed.' }

# This isolated test MIDlet is never added to the First Contact package.
$jar = Join-Path $out 'ogm-outbox-test.jar'
Copy-Item -LiteralPath $verified -Destination $jar
$metadata = @"
Manifest-Version: 1.0
MIDlet-Name: OGM Outbox Test
MIDlet-Version: 0.0.1
MIDlet-Vendor: OGM
MIDlet-1: OGM Outbox Test,,ogm.delivery.v1.RmsOutboxTest
MicroEdition-Configuration: CLDC-1.1
MicroEdition-Profile: MIDP-2.0

"@
$manifest = Join-Path $out 'MANIFEST.MF'
[IO.File]::WriteAllText($manifest, $metadata.Replace("`r`n", "`n").Replace("`n", "`r`n"), [Text.Encoding]::ASCII)
& (Join-Path $jdk 'jar.exe') ufm $jar $manifest
if ($LASTEXITCODE -ne 0) { throw 'Outbox test MIDlet packaging failed.' }
$jad = Join-Path $out 'ogm-outbox-test.jad'
foreach ($phase in @('provision', 'retry', 'resolved')) {
    $properties = $metadata.TrimEnd() + "`nMIDlet-Jar-URL: ogm-outbox-test.jar`nMIDlet-Jar-Size: $((Get-Item -LiteralPath $jar).Length)`nOGM-State-Phase: $phase`n"
    [IO.File]::WriteAllText($jad, $properties.Replace("`r`n", "`n").Replace("`n", "`r`n"), [Text.Encoding]::ASCII)
    $log = Join-Path $out "$phase.stdout.log"
    $errorLog = Join-Path $out "$phase.stderr.log"
    $java = Join-Path $jdk 'java.exe'
    # user.home is redirected only in the child JVM; all emulator files are local.
    $arguments = @('-Djava.awt.headless=true', ('-Duser.home="' + $emulatorHome + '"'), '-cp', ('"' + (Join-Path $tools 'microemulator-2.0.4.jar') + '"'), 'org.microemu.app.Headless', '--rms', 'file', '--quit', ('"' + $jad + '"'))
    $process = Start-Process -FilePath $java -ArgumentList $arguments -WindowStyle Hidden -PassThru -RedirectStandardOutput $log -RedirectStandardError $errorLog
    # Keep the native process handle so Windows PowerShell 5.1 retains ExitCode.
    $null = $process.Handle
    if (-not $process.WaitForExit(20000)) {
        Stop-Process -Id $process.Id
        throw "Outbox emulator timed out in $phase; see $out"
    }
    $process.WaitForExit()
    $result = Get-Content -LiteralPath $log -Raw
    if ($process.ExitCode -ne 0 -or $result -notmatch "RMS-OUTBOX-PASS: $phase" -or $result -match 'RMS-OUTBOX-FAIL') {
        Get-Content -LiteralPath $log, $errorLog
        throw "Outbox file-RMS emulator phase failed: $phase (exit $($process.ExitCode))"
    }
    Write-Output "PASS: MicroEmulator file-RMS phase $phase (separate JVM)."
}
Write-Output "PASS: outbox CLDC 1.1/MIDP 2.0 compilation, preverification, state tests and three JVM RMS restart phases. Output: $out"
