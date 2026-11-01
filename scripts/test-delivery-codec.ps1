$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$tools = Join-Path $root '.tools/java-me'
$jdk = Join-Path $tools 'jdk/jdk8u504-b01/bin'
$apis = (Join-Path $tools 'cldcapi11-2.0.4.jar') + ';' + (Join-Path $tools 'midpapi20-2.0.4.jar')
# A separate output and source tree keep the validated MIDlet package unchanged.
$out = Join-Path $root ('.build/delivery-v1/' + [Guid]::NewGuid().ToString('N'))
$classes = Join-Path $out 'classes'
$tests = Join-Path $out 'tests'
New-Item -ItemType Directory -Force -Path $classes, $tests | Out-Null
$sources = @(
    (Join-Path $root 'clients/java-me/src/ogm/Utf8.java'),
    (Join-Path $root 'clients/java-me/delivery-v1/ogm/delivery/v1/Codec.java')
)
& (Join-Path $jdk 'javac.exe') -encoding UTF-8 -source 1.3 -target 1.1 -bootclasspath $apis -d $classes @sources
if ($LASTEXITCODE -ne 0) { throw 'Delivery codec CLDC compilation failed.' }
$raw = Join-Path $out 'raw.jar'
$verified = Join-Path $out 'verified.jar'
& (Join-Path $jdk 'jar.exe') cMf $raw -C $classes .
if ($LASTEXITCODE -ne 0) { throw 'Delivery codec intermediate packaging failed.' }
& (Join-Path $jdk 'java.exe') -jar (Join-Path $tools 'proguard-base-6.2.2.jar') -injars $raw -outjars $verified -libraryjars $apis -microedition -target 1.1 -dontshrink -dontoptimize -dontobfuscate -keep 'class ogm.** { *; }'
if ($LASTEXITCODE -ne 0) { throw 'Delivery codec CLDC preverification failed.' }
foreach ($name in @('ogm.delivery.v1.Codec', 'ogm.delivery.v1.Codec$Frame', 'ogm.Utf8')) {
    $details = & (Join-Path $jdk 'javap.exe') -v -classpath $verified $name
    if ($LASTEXITCODE -ne 0 -or -not ($details -match 'major version: 45') -or -not ($details -match 'minor version: 3')) { throw "Unexpected class version: $name" }
    if ($name -ne 'ogm.delivery.v1.Codec$Frame' -and -not ($details -match 'StackMap:')) { throw "Missing CLDC StackMap: $name" }
}
& (Join-Path $jdk 'javac.exe') -encoding UTF-8 -cp $verified -d $tests (Join-Path $root 'clients/java-me/test/DeliveryCodecTest.java')
if ($LASTEXITCODE -ne 0) { throw 'Delivery codec desktop test compilation failed.' }
& (Join-Path $jdk 'java.exe') -cp ($tests + ';' + $verified) DeliveryCodecTest (Join-Path $root 'testdata/delivery-v1/fixtures.txt')
if ($LASTEXITCODE -ne 0) { throw 'Delivery codec shared fixtures failed.' }
Write-Output "PASS: CLDC/MIDP-only compile, preverification, class versions 45.3, and desktop execution of preverified classes. Output: $out"
