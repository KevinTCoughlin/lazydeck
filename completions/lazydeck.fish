# Fish completion for lazydeck.
complete -c lazydeck -f
complete -c lazydeck -n '__fish_use_subcommand' -a serve -d 'Run the local engine-integration API'
complete -c lazydeck -n '__fish_use_subcommand' -a version -d 'Print version and build metadata'
complete -c lazydeck -n '__fish_use_subcommand' -a help -d 'Show usage information'
complete -c lazydeck -n '__fish_seen_subcommand_from serve' -l fixture -d 'Use the in-memory test backend'
complete -c lazydeck -s h -l help -d 'Show usage information'
complete -c lazydeck -s v -l version -d 'Print version and build metadata'
