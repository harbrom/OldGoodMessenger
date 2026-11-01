"""Build/validate the offline handset JAR and drive it in separate emulator JVMs."""
import hashlib
from pathlib import Path
import subprocess
import uuid
import zipfile

ROOT = Path(__file__).resolve().parent.parent
TOOLS = ROOT / '.tools/java-me'
JDK = TOOLS / 'jdk/jdk8u504-b01/bin'
OUT = ROOT / '.build/java-me/ogm-storage-smoke'


def run(args, **kwargs):
    return subprocess.run([str(x) for x in args], cwd=ROOT, check=True, timeout=60, **kwargs)


def attrs(text):
    text = text.replace('\r\n', '\n').replace('\n ', '')
    return dict(line.split(': ', 1) for line in text.splitlines() if ': ' in line)


def main():
    original = ROOT / '.build/java-me/ogm-first-contact'
    hashes = {p: hashlib.sha256(p.read_bytes()).digest() for p in original.glob('ogm-first-contact.*')}
    run(['powershell', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', 'scripts/build-k750i-smoke.ps1'])
    jar, jad = OUT / 'ogm-storage-smoke.jar', OUT / 'ogm-storage-smoke.jad'
    properties = attrs(jad.read_text('ascii'))
    assert int(properties['MIDlet-Jar-Size']) == jar.stat().st_size
    assert properties['MIDlet-Jar-URL'] == jar.name
    with zipfile.ZipFile(jar) as package:
        manifest = attrs(package.read('META-INF/MANIFEST.MF').decode('ascii'))
        for key, value in properties.items():
            if key not in ('MIDlet-Jar-Size', 'MIDlet-Jar-URL'):
                assert manifest[key] == value, key
        assert manifest['MIDlet-1'] == 'OGM Storage Smoke,,ogm.delivery.smoke.StorageSmoke'
        assert manifest['MicroEdition-Configuration'] == 'CLDC-1.1'
        assert manifest['MicroEdition-Profile'] == 'MIDP-2.0'
        assert not any('Permission' in key for key in properties)
        for name in package.namelist():
            if name.endswith('.class'):
                data = package.read(name)
                assert data[:8] == bytes.fromhex('cafebabe0003002d'), name
                assert b'javax/microedition/io/' not in data, name
                assert b'org/microemu' not in data, name
    work = ROOT / '.build/k750i-smoke-test' / uuid.uuid4().hex
    classes, home = work / 'driver', work / 'emulator-home'
    classes.mkdir(parents=True)
    home.mkdir()
    emulator = TOOLS / 'microemulator-2.0.4.jar'
    run([JDK / 'javac.exe', '-encoding', 'UTF-8', '-cp', emulator, '-d', classes,
         ROOT / 'clients/java-me/test/StorageSmokeUI.java'])
    for phase in ('fresh', 'provision', 'reopen', 'corrupt', 'corrupt-reopen'):
        completed = subprocess.run([str(x) for x in [JDK / 'java.exe', '-Djava.awt.headless=true', '-Dfile.encoding=UTF-8',
                                   '-Duser.home=' + str(home), '-cp', str(classes) + ';' + str(emulator),
                                   'StorageSmokeUI', jad, phase]], cwd=ROOT, timeout=20,
                                   capture_output=True, encoding='utf-8', errors='replace')
        (work / (phase + '.stdout.log')).write_text(completed.stdout, 'utf-8')
        (work / (phase + '.stderr.log')).write_text(completed.stderr, 'utf-8')
        assert completed.returncode == 0, completed.stdout + completed.stderr
        assert 'SMOKE-UI-PASS: ' + phase in completed.stdout, completed.stdout + completed.stderr
        assert 'SMOKE-UI-FAIL:' not in completed.stdout
        print('\n'.join(line for line in completed.stdout.splitlines()
                        if line.startswith(('SMOKE-UI-', 'timing:', 'provision:', 'initialize:', 'history:', 'reserve:', 'verify:'))))
    for path, expected in hashes.items():
        assert hashlib.sha256(path.read_bytes()).digest() == expected, path
    print('PASS: metadata, offline dependencies, five JVM UI/file-RMS phases; First Contact artifacts unchanged.')
    print('Logs:', work)


if __name__ == '__main__':
    main()
