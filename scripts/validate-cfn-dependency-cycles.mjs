#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { basename } from 'node:path';

function usage() {
  console.error('usage: validate-cfn-dependency-cycles.mjs <template.json>');
}

const [templatePath, ...extra] = process.argv.slice(2);
if (!templatePath || extra.length > 0) {
  usage();
  process.exit(2);
}

let template;
try {
  template = JSON.parse(readFileSync(templatePath, 'utf8'));
} catch (error) {
  console.error(`failed to read CloudFormation template ${templatePath}: ${error.message}`);
  process.exit(2);
}

const resources = template && typeof template === 'object' && !Array.isArray(template)
  ? template.Resources
  : undefined;
if (!resources || typeof resources !== 'object' || Array.isArray(resources)) {
  console.error(`CloudFormation template ${templatePath} has no Resources object`);
  process.exit(2);
}

const resourceIds = Object.keys(resources).sort();
const resourceSet = new Set(resourceIds);
const edges = new Map();
const edgeReasons = new Map();

function ensureSource(source) {
  if (!edges.has(source)) {
    edges.set(source, new Set());
  }
}

for (const resourceId of resourceIds) {
  ensureSource(resourceId);
}

function addEdge(source, target, reason) {
  if (!resourceSet.has(target) || source === target) {
    return;
  }
  ensureSource(source);
  edges.get(source).add(target);
  const key = `${source}\0${target}`;
  const reasons = edgeReasons.get(key) ?? [];
  reasons.push(reason);
  edgeReasons.set(key, reasons);
}

function normalizePathSegment(segment) {
  return /^[A-Za-z_][A-Za-z0-9_]*$/.test(segment) ? `.${segment}` : `[${JSON.stringify(segment)}]`;
}

function childPath(parent, segment) {
  if (typeof segment === 'number') {
    return `${parent}[${segment}]`;
  }
  return `${parent}${normalizePathSegment(segment)}`;
}

const subVariablePattern = /\$\{([A-Za-z0-9:.]+)\}/g;

function addSubEdges(source, value, explicitVariables, path) {
  for (const match of value.matchAll(subVariablePattern)) {
    const variable = match[1];
    if (!variable || variable.startsWith('AWS::') || explicitVariables.has(variable)) {
      continue;
    }
    addEdge(source, variable.split('.')[0], `Fn::Sub ${match[0]} at ${path}`);
  }
}

function visit(source, value, path) {
  if (Array.isArray(value)) {
    value.forEach((entry, index) => visit(source, entry, childPath(path, index)));
    return;
  }
  if (!value || typeof value !== 'object') {
    return;
  }

  if (typeof value.Ref === 'string') {
    addEdge(source, value.Ref, `Ref at ${childPath(path, 'Ref')}`);
  }

  if (Object.prototype.hasOwnProperty.call(value, 'Fn::GetAtt')) {
    const getAtt = value['Fn::GetAtt'];
    if (typeof getAtt === 'string') {
      addEdge(source, getAtt.split('.')[0], `Fn::GetAtt at ${childPath(path, 'Fn::GetAtt')}`);
    } else if (Array.isArray(getAtt) && getAtt.length > 0) {
      if (typeof getAtt[0] === 'string') {
        addEdge(source, getAtt[0], `Fn::GetAtt at ${childPath(path, 'Fn::GetAtt')}`);
      } else {
        visit(source, getAtt[0], childPath(childPath(path, 'Fn::GetAtt'), 0));
      }
    }
  }

  if (Object.prototype.hasOwnProperty.call(value, 'Fn::Sub')) {
    const sub = value['Fn::Sub'];
    if (typeof sub === 'string') {
      addSubEdges(source, sub, new Set(), childPath(path, 'Fn::Sub'));
    } else if (Array.isArray(sub) && sub.length > 0) {
      const explicitVariables = sub[1] && typeof sub[1] === 'object' && !Array.isArray(sub[1])
        ? new Set(Object.keys(sub[1]))
        : new Set();
      if (typeof sub[0] === 'string') {
        addSubEdges(source, sub[0], explicitVariables, childPath(path, 'Fn::Sub'));
      } else {
        visit(source, sub[0], childPath(childPath(path, 'Fn::Sub'), 0));
      }
    }
  }

  for (const [key, nested] of Object.entries(value)) {
    visit(source, nested, childPath(path, key));
  }
}

for (const [resourceId, resource] of Object.entries(resources)) {
  const dependsOn = resource && typeof resource === 'object' ? resource.DependsOn : undefined;
  if (typeof dependsOn === 'string') {
    addEdge(resourceId, dependsOn, 'DependsOn');
  } else if (Array.isArray(dependsOn)) {
    for (const dependency of dependsOn) {
      if (typeof dependency === 'string') {
        addEdge(resourceId, dependency, 'DependsOn');
      }
    }
  }
  visit(resourceId, resource, `Resources.${resourceId}`);
}

const indices = new Map();
const lowlinks = new Map();
const stack = [];
const onStack = new Set();
const components = [];
let nextIndex = 0;

function strongConnect(node) {
  indices.set(node, nextIndex);
  lowlinks.set(node, nextIndex);
  nextIndex += 1;
  stack.push(node);
  onStack.add(node);

  for (const dependency of [...(edges.get(node) ?? [])].sort()) {
    if (!indices.has(dependency)) {
      strongConnect(dependency);
      lowlinks.set(node, Math.min(lowlinks.get(node), lowlinks.get(dependency)));
    } else if (onStack.has(dependency)) {
      lowlinks.set(node, Math.min(lowlinks.get(node), indices.get(dependency)));
    }
  }

  if (lowlinks.get(node) === indices.get(node)) {
    const component = [];
    let member;
    do {
      member = stack.pop();
      onStack.delete(member);
      component.push(member);
    } while (member !== node);
    components.push(component.sort());
  }
}

for (const resourceId of resourceIds) {
  if (!indices.has(resourceId)) {
    strongConnect(resourceId);
  }
}

const cyclicComponents = components
  .filter((component) => component.length > 1 || component.some((resourceId) => edges.get(resourceId)?.has(resourceId)))
  .sort((a, b) => a[0].localeCompare(b[0]));

const edgeCount = [...edges.values()].reduce((sum, dependencies) => sum + dependencies.size, 0);

function edgeKey(source, target) {
  return `${source}\0${target}`;
}

function findCyclePath(component) {
  const componentSet = new Set(component);
  for (const start of component) {
    const path = [start];
    const onPath = new Set([start]);

    function dfs(node) {
      const dependencies = [...(edges.get(node) ?? [])]
        .filter((dependency) => componentSet.has(dependency))
        .sort();
      for (const dependency of dependencies) {
        if (dependency === start) {
          return [...path, start];
        }
        if (onPath.has(dependency)) {
          continue;
        }
        onPath.add(dependency);
        path.push(dependency);
        const found = dfs(dependency);
        if (found) {
          return found;
        }
        path.pop();
        onPath.delete(dependency);
      }
      return undefined;
    }

    const found = dfs(start);
    if (found) {
      return found;
    }
  }
  return component;
}

if (cyclicComponents.length === 0) {
  console.log(
    `CloudFormation dependency graph OK: no circular dependencies in ${basename(templatePath)} ` +
      `(${resourceIds.length} resources, ${edgeCount} edges).`,
  );
  process.exit(0);
}

console.error(
  `CloudFormation dependency cycle detected in ${basename(templatePath)} ` +
    `(${resourceIds.length} resources, ${edgeCount} edges).`,
);
for (const [index, component] of cyclicComponents.entries()) {
  const cyclePath = findCyclePath(component);
  console.error(`\nCycle ${index + 1}: ${cyclePath.join(' -> ')}`);
  console.error('Cycle edge evidence:');
  for (let i = 0; i < cyclePath.length - 1; i += 1) {
    const source = cyclePath[i];
    const target = cyclePath[i + 1];
    const reasons = edgeReasons.get(edgeKey(source, target)) ?? ['<implicit dependency>'];
    console.error(`- ${source} -> ${target}: ${reasons.slice(0, 3).join('; ')}`);
  }
  console.error('Strongly connected resources:');
  for (const resourceId of component) {
    console.error(`- ${resourceId}`);
  }
  console.error('All internal dependency edges:');
  for (const source of component) {
    for (const target of [...(edges.get(source) ?? [])].filter((candidate) => component.includes(candidate)).sort()) {
      const reasons = edgeReasons.get(edgeKey(source, target)) ?? ['<implicit dependency>'];
      console.error(`- ${source} -> ${target}: ${reasons.slice(0, 3).join('; ')}`);
    }
  }
}
process.exit(1);
