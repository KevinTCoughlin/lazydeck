# Bash completion for lazydeck.
_lazydeck()
{
    local cur
    cur="${COMP_WORDS[COMP_CWORD]}"

    case "${COMP_CWORD}" in
        1)
            COMPREPLY=($(compgen -W 'serve api-key version help --help --version -h -v' -- "${cur}"))
            ;;
        2)
            if [[ "${COMP_WORDS[1]}" == "serve" ]]; then
                COMPREPLY=($(compgen -W '--fixture --port --listen --registry --help -h' -- "${cur}"))
            elif [[ "${COMP_WORDS[1]}" == "api-key" ]]; then
                COMPREPLY=($(compgen -W 'create list revoke' -- "${cur}"))
            elif [[ "${COMP_WORDS[1]}" == "version" ]]; then
                COMPREPLY=($(compgen -W '--check' -- "${cur}"))
            fi
            ;;
    esac
}

complete -F _lazydeck lazydeck
