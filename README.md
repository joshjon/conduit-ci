# Conduit

A quick PoC to test out an old idea.

## Brain dump

### Runners

1. User creates pipeline definition which comprises of:

    - Singe pipeline (workflow)
    - Many jobs (activity)
    - Queryable module dependencies
    - Worker registered with pipeline and job definitions

2. There will need to be at least 1 Conduit runner available installed with the conduit-cli (like how dagger does) that will:

    - Start the Conduit parent orchestrator worker/workflow
    - Look at the repository config file to know what language to build and run in e.g. similar to Dagger

    ```json
    {
      "name": "hello-dagger",
      "engineVersion": "v0.19.0",
      "sdk": {
        "source": "go"
      },
      "source": ".dagger"
    }
    ```
   - Orchestrator will checkout/build/run the repository's pipeline worker
   - Execute the pipeline workflow (needs to know the name of it probably via the config) that immediately waits for a signal to resume
   - Query the pipeline to know what additional module workers to download/build/run
   - Signal the pipeline to resume now that the dependant module workers are running
   - Wait for the pipeline to finish and kill all the worker containers