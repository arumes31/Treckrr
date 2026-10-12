import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

/** Classifies changed paths as review candidates, never as proven safe changes. */
export function classify(files) {
  const groups = { migrations: [], configuration: [], deployment: [], billing: [], security: [], other: [] };
  for (const file of files) {
    const p = file.path;
    if (/^internal\/db\/migrations\//.test(p)) groups.migrations.push(file);
    else if (/^(\.env|internal\/config\/)|^compose.*\.yml$|^docker-compose.*\.yml$/.test(p)) groups.configuration.push(file);
    else if (/Dockerfile$|^\.github\/|^deploy/.test(p)) groups.deployment.push(file);
    else if (/^internal\/(calc|billing|einvoice)\/|(?:invoice|payment|rollover|booking|beleg)/.test(p)) groups.billing.push(file);
    else if (/auth|session|csrf|passkey|webauthn|backup/.test(p)) groups.security.push(file);
    else groups.other.push(file);
  }
  return groups;
}

export function configCandidates(diff) {
  return [...new Set(diff.split('\n').filter((line) => /^[+-](?![+-])/.test(line))
    .flatMap((line) => line.match(/\b[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+\b/g) || []))].sort();
}

export function reviewMatches(review, base, head) {
  return review?.baseCommit === base && review?.headCommit === head &&
    ['productionImpact', 'manualSteps', 'rollback'].every((key) =>
      typeof review[key] === 'string' && review[key].trim().length > 0 && review[key].length <= 10000);
}

function escape(value) { return String(value).replace(/[\r\n|`<>]/g, ' '); }

/** Keeps human conclusions and automatic path/config hints visibly separate. */
export function renderReport({ base, head, mergeBase, files, variables, review }) {
  const confirmed = reviewMatches(review, base, head);
  const lines = ['# Release-Auswirkungen', '',
    `Vergleich: \`${base}\` → \`${head}\` (gemeinsamer Ausgangspunkt \`${mergeBase}\`).`, '',
    '## Fachliche Freigabe', '',
    confirmed ? '**GEPRÜFT für genau diesen Vergleich.**' : '**OFFEN — Produktionsauswirkungen, manuelle Schritte und Rückweg sind nicht bestätigt.**', '',
    'Automatische Dateierkennung ist keine Kompatibilitätsprüfung und keine Aussage „keine manuellen Schritte“.', ''];
  for (const [key, label] of [['productionImpact', 'Produktionsauswirkungen'], ['manualSteps', 'Manuelle Schritte'], ['rollback', 'Rückweg / Einschränkungen']]) {
    lines.push(`- **${label}:** ${confirmed ? escape(review[key]) : 'Noch fachlich prüfen.'}`);
  }
  lines.push('', '## Automatisch erkannte Prüfkandidaten', '');
  const labels = { migrations: 'Datenbankmigrationen (laufen beim App-Start)', configuration: 'Konfiguration / Compose', deployment: 'Deployment / CI', billing: 'Abrechnung / Geldbeträge', security: 'Sicherheit / Backup', other: 'Weitere Änderungen' };
  for (const [key, values] of Object.entries(classify(files))) {
    lines.push(`### ${labels[key]} (${values.length})`, '');
    if (!values.length) lines.push('Keine über Dateinamen erkannt.');
    else for (const file of values) lines.push(`- \`${escape(file.status)}\` \`${escape(file.path)}\``);
    lines.push('');
  }
  lines.push('### Konfigurationsbezeichner in geänderten Zeilen', '',
    variables.length ? variables.map((key) => `\`${escape(key)}\``).join(', ') : 'Keine automatisch erkannt.', '',
    'Auch entfernte und umbenannte Dateien prüfen. Die Erkennung kann semantische Änderungen, Datenkorrekturen und indirekte Abhängigkeiten nicht vollständig erfassen.', '',
    'Vor Deployment: Sicherung/Wiederherstellung prüfen; Migrationen, neue Pflichtwerte und Rückwärtskompatibilität fachlich bewerten. Kein automatisches Merge, Push oder Deployment.', '');
  return lines.join('\n');
}

function git(args) { return execFileSync('git', args, { encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 }); }
function commit(ref) {
  if (!ref || ref.startsWith('-')) throw new Error('invalid revision');
  return git(['rev-parse', '--verify', `${ref}^{commit}`]).trim();
}

export function main(args = process.argv.slice(2)) {
  let baseRef = 'main', headRef = 'dev', reviewPath, check = false;
  for (let i = 0; i < args.length; i++) {
    if (args[i] === '--base') baseRef = args[++i];
    else if (args[i] === '--head') headRef = args[++i];
    else if (args[i] === '--review') reviewPath = args[++i];
    else if (args[i] === '--check') check = true;
    else throw new Error(`unknown option: ${args[i]}`);
  }
  const base = commit(baseRef), head = commit(headRef);
  const mergeBase = git(['merge-base', base, head]).trim();
  const fields = git(['diff', '--name-status', '--no-renames', '-z', mergeBase, head, '--']).split('\0');
  const files = [];
  for (let i = 0; i + 1 < fields.length; i += 2) files.push({ status: fields[i], path: fields[i + 1] });
  const diff = git(['diff', '--unified=0', mergeBase, head, '--', 'internal/config', '.env.example', '*compose*.yml', 'Dockerfile']);
  const review = reviewPath ? JSON.parse(readFileSync(reviewPath, 'utf8')) : undefined;
  process.stdout.write(renderReport({ base, head, mergeBase, files, variables: configCandidates(diff), review }));
  if (check && !reviewMatches(review, base, head)) process.exitCode = 1;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try { main(); } catch (error) { console.error(`release-impact: ${error.message}`); process.exitCode = 1; }
}
