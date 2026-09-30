use strict;
use warnings;
use Cwd qw(getcwd);

my $payload = do { local $/; open my $f, '<', $ENV{KICKD_PAYLOAD_FILE} or die "$!"; <$f> };
print "event=$ENV{KICKD_EVENT}\n";
print "trigger=$ENV{KICKD_TRIGGER}\n";
print "msg=$ENV{KICKD_DATA_MSG}\n";
print "dir=" . getcwd() . "\n";
print "payload=$payload\n";
print STDERR "to stderr\n";
exit $ENV{KICKD_DATA_CODE};
