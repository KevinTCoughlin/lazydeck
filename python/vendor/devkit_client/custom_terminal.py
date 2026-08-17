import logging
import shutil
import subprocess

logger = logging.getLogger(__name__)

# Support the use of conemu or cmder on Windows
# glue between config settings and the low level run_in_terminal logic
class CustomTerminal:
    TERM_KEY = 'CustomTerminal.Term'
    STYLE_KEY = 'CustomTerminal.Style' # 0: disabled, 1: conemu, 2: cmder

    def __init__(self):
        self.conf = None
        self.settings = None

    def setup(self, conf, settings):
        self.conf = conf
        self.settings = settings

        # ensure default values
        if self.settings.get(self.STYLE_KEY, None) is None:
            self.settings[self.STYLE_KEY] = 0
            self.settings[self.TERM_KEY] = None

        matched = False
        for (term_attr, style) in (('with_conemu', 1), ('with_cmder', 2)):
            term = vars(self.conf).get(term_attr)
            if term is None:
                continue
            matched = True
            if term == "":
                # Explicit disable
                style = 0
                break
            if len(term) > 0:
                # Can pass a full path, or we will lookup here
                term = shutil.which(term)
                if term is not None:
                    break

        if not matched:
            # settings are unchanged, the command line options were not used
            return

        if style == 0 or term is None:
            logger.info(f'Custom windows terminal is disabled')
            self.settings[self.STYLE_KEY] = 0
            return

        logger.info(f'Using custom windows terminal: {term}')
        self.settings[self.STYLE_KEY] = style
        self.settings[self.TERM_KEY] = term

    def Popen(self, commands):
        if self.settings[self.STYLE_KEY] == 0:
            return

        if self.settings[self.STYLE_KEY] == 1:
            commands = [self.settings[self.TERM_KEY], '-run'] + commands
        elif self.settings[self.STYLE_KEY] == 2:
            commands = [self.settings[self.TERM_KEY], '/x', '-run ' + ' '.join(commands)]
        else:
            raise Exception(f'Unexpected STYLE_KEY {self.settings[self.STYLE_KEY]} value in CustomTerminal')

        logging.info(f'Run in terminal - custom terminal: {commands!r}')
        p = subprocess.Popen(
            commands,
        )
        return p
