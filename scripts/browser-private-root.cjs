'use strict';
const fs = require('node:fs');
const path = require('node:path');

// The caller may supply an already ACL-restricted directory. Default browser
// runs retain their existing workspace/.local behavior.
function resolveBrowserPrivateRoot(workspace, requested) {
  const root = fs.realpathSync(workspace);
  const localPath = path.join(root, '.local');
  fs.mkdirSync(localPath, {recursive: true});
  const local = fs.realpathSync(localPath);
  if (!local.startsWith(root + path.sep) || !fs.statSync(local).isDirectory()) throw Error('Private browser directory leaves the workspace');
  if (requested === undefined || requested === '') return local;
  if (typeof requested !== 'string' || requested.includes('\0')) throw Error('Invalid private browser root');
  // Explicit roots must exist: this resolver cannot establish their OS ACLs.
  const selected = fs.realpathSync(path.resolve(root, requested));
  if ((selected !== local && !selected.startsWith(local + path.sep)) || !fs.statSync(selected).isDirectory()) throw Error('Explicit private browser root must be an existing directory inside workspace .local');
  return selected;
}

module.exports = {resolveBrowserPrivateRoot};
