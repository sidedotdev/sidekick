# Scripted Agent Requirements

## Overview

When creating a task, there is a new flow type available corresponding to a
scripted agent. When this is selected, a new flow option is available to provide
the script. This script will drive the agent's workflow. This is a programmatic
version of process-oriented skills, which is much more reliable than LLM-driven
workflows based on natural language. This can also be used to build customized
verification loops or other agent workflows.

## High-Level Requirements

- Each script has a name, so that it can be saved in the client as a reusable &
editable preset
- Scripts may optionally refer to files in the repo instead of a string, in
which case they should be auto-discovered via some convention or repo config
(TBD)
- Frontend script editor has syntax highlighting, autocomplete & typechecking.
Reuse libs powering things like typescript playground.

## Tech Requirements & Constraints

- The way the LLM is

- We'd lik
    

### Defining scripts

- Typescript only

### Running scripts

- We use https://github.com/dop251/goja