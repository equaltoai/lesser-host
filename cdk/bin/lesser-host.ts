#!/usr/bin/env node
import * as cdk from 'aws-cdk-lib';
import { LesserHostStack } from '../lib/lesser-host-stack';

const app = new cdk.App();

const stage = (app.node.tryGetContext('stage') as string | undefined) ?? 'lab';

new LesserHostStack(app, `lesser-host-${stage}`, { stage });
