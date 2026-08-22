# Bash completion for lazydeck.
_lazydeck()
{
    local cur
    cur="${COMP_WORDS[COMP_CWORD]}"

    case "${COMP_CWORD}" in
        1)
            COMPREPLY=($(compgen -W 'serve version help --help --version -h -v' -- "${cur}"))
            ;;
        2)
            if [[ "${COMP_WORDS[1]}" == "serve" ]]; then
                COMPREPLY=($(compgen -W '--fixture --help -h' -- "${cur}"))
            fi
            ;;
    esac
}

complete -F _lazydeck lazydeck
