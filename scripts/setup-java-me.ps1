$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$root = Split-Path -Parent $PSScriptRoot
$tools = Join-Path $root '.tools'
$javaME = Join-Path $tools 'java-me'
New-Item -ItemType Directory -Force -Path $javaME | Out-Null
function Get-VerifiedFile($url, $path, $hash) {
    if (-not (Test-Path -LiteralPath $path)) {
        Invoke-WebRequest -UseBasicParsing $url -OutFile $path
    }
    if ((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant() -ne $hash) {
        throw "Checksum mismatch: $path"
    }
}
$jdkZip = Join-Path $tools 'OpenJDK8U-jdk_x64_windows_hotspot_8u504b01.zip'
Get-VerifiedFile 'https://github.com/adoptium/temurin8-binaries/releases/download/jdk8u504-b01/OpenJDK8U-jdk_x64_windows_hotspot_8u504b01.zip' $jdkZip 'ea43d46ede95b51e44a12c66711706cddc762e0a766c54bccea18954e902b2aa'
$jdkRoot = Join-Path $javaME 'jdk'
if (-not (Test-Path -LiteralPath (Join-Path $jdkRoot 'jdk8u504-b01'))) {
    Expand-Archive -LiteralPath $jdkZip -DestinationPath $jdkRoot
}
$artifacts = @(
    @('org/microemu/microemulator/2.0.4/microemulator-2.0.4.jar', 'dbd5f3eb8365d3e839d6a203149e0e3776fc1a0585e16ac1fc23f76c9fcae1c6'),
    @('org/microemu/cldcapi11/2.0.4/cldcapi11-2.0.4.jar', 'f86f41bfb4097e4bd9cdfeca528960f6977cbda0f5785342adef9909a6a3baef'),
    @('org/microemu/midpapi20/2.0.4/midpapi20-2.0.4.jar', 'b7f6ed5e0344c7000643ecc114e6edff3587e8e77face546af0e12ffd6dbe826'),
    @('net/sf/proguard/proguard-base/6.2.2/proguard-base-6.2.2.jar', '7587a3a415d6aea1946f597f4babe710a245f085bac4dc5e7cda653acfc39b03')
)
foreach ($artifact in $artifacts) {
    Get-VerifiedFile ("https://repo.maven.apache.org/maven2/" + $artifact[0]) (Join-Path $javaME (Split-Path -Leaf $artifact[0])) $artifact[1]
}
& (Join-Path $jdkRoot 'jdk8u504-b01/bin/java.exe') -version
if ($LASTEXITCODE -ne 0) { throw 'JDK version check failed.' }
