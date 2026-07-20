Development rules
=================

General guidelines
------------------

 1. Always start off by considering state-of-the-art linters and build checks.
 2. Refer to ``.agent-docs/*`` for understanding the environment.
 3. Use the tools described in this file to find work to do, structure the work,
    coordinate the work with other agents, and record the work and follow-up
    tasks.
 4. If you are the Codex Agent: All work must be done in branches labeled
    ``codex/[beads-ticket-id]/[short-description]``.
 5. If you are the Claude Agent: All work must be done in branches labeled
    ``claude/[beads-ticket-id]/[short-description]``.
 6. If you are the Copilot Agent: All work must be done in branches labeled
    ``copilot/[beads-ticket-id]/[short-description]``.
 7. Ask, don't assume. If something is unclear, ask before writing a single
    line. Never make silent assumptions about intent, architecture, or 
    requirements. When running unattended, pick the most reasonable 
    interpretation, proceed, and record the assumption rather than blocking.
 8. Implement the simplest solution for simple problems, better solutions for
    harder problems. Do not over-engineer or add flexibility that isn't needed
    yet.
 9. Don't touch unrelated code but please do surface bad code or design smells
    you discover with me so we can address them as a separate issue.
10. Flag uncertainty explicitly. If you're unsure about something, see point 1
    above. If it makes sense to do so, conduct a small, localised and low-risk
    experiment and bring the hypothesis and results to me to discuss.
    Confidence without certainty causes more damage than admitting a gap.
11. I'm always open to ideas on better ways to do things. Please don't hesitate
    to suggest a better way, or one that has long lasting impact over a
    tactical change. (as a few examples)


Building phase
--------------

1. NEVER take shortcuts. NEVER skip quality gates or review steps.
2. ALWAYS run linters, tests, and builds locally before pushing.
3. ALWAYS update documentation and tests during work.
4. ALWAYS file issues for follow-up work if you can't complete everything in
   one session.
5. ALWAYS start from tests and implement software that satisfies the original
   intent of a task AND the test.


Tools
=====

Tool: Beads (Ticket tracking and project management)
----------------------------------------------------

Agent Instructions
~~~~~~~~~~~~~~~~~~
This project uses **bd** (`beads <steveyegge_beads_>`__) for issue tracking.
Run ``bd prime`` for full workflow context.


Quick Reference
~~~~~~~~~~~~~~~

.. code-block:: bash

    bd ready                # Find available work
    bd show <id>            # View issue details
    bd update <id> --claim  # Claim work atomically
    bd close <id>           # Complete work
    bd dolt push            # Push beads data to remote


Non-Interactive Shell Commands
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

**ALWAYS use non-interactive flags** with file operations to avoid hanging on
confirmation prompts.

Shell commands like ``cp``, ``mv``, and ``rm`` may be aliased to include ``-i``
(interactive) mode on some systems, causing the agent to hang indefinitely
waiting for y/n input.

**Use these forms instead:**

.. code-block:: bash

    # Force overwrite without prompting
    cp -f source dest           # NOT: cp source dest
    mv -f source dest           # NOT: mv source dest
    rm -f file                  # NOT: rm file

    # For recursive operations
    rm -rf directory            # NOT: rm -r directory
    cp -rf source dest          # NOT: cp -r source dest

**Other commands that may prompt:**

- ``scp`` - use ``-o BatchMode=yes`` for non-interactive
- ``ssh`` - use ``-o BatchMode=yes`` to fail instead of prompting
- ``apt`` - use ``-y`` flag
- ``brew`` - use ``HOMEBREW_NO_AUTO_UPDATE=1`` env var

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->

Rules
~~~~~

- Use ``bd`` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or
  markdown TODO lists
- Run ``bd prime`` for detailed command reference and session close protocol
- Use ``bd remember`` for persistent knowledge — do NOT use MEMORY.md files

Session Completion
~~~~~~~~~~~~~~~~~~

**When ending a work session**, you MUST complete ALL steps below. Work is NOT
complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:

   .. code-block:: bash

       git pull --rebase
       git push
       git status  # MUST show "up to date with origin"

5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**

- Work is NOT complete until ``git push`` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds


Tool: notify-jonas (Owner-operator notifier)
--------------------------------------------

If you need to send a message to the system operator you can use the
notify-jonas skill, which notifies me (the owner-operator). You should also
use this to keep me appraised of reaching major milestones, running into
unexpected blockers, or when you need input or sandbox escalations.


.. _steveyegge_beads: https://github.com/steveyegge/beads
