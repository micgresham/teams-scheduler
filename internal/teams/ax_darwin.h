#include <ApplicationServices/ApplicationServices.h>

// Normalised roles reported to Go.
enum { TSS_BUTTON = 1, TSS_MENUITEM, TSS_RADIO, TSS_CHECKBOX, TSS_LINK, TSS_LISTITEM };

typedef struct {
	AXUIElementRef ref; // retained; release with tss_release
	int role;
	char name[512];
} TSSItem;

int tss_trusted(int prompt);
int tss_pid_for_bundle(const char *bundleID);
int tss_is_hidden(int pid);
void tss_set_hidden(int pid, int hidden);
int tss_frontmost_pid(void);
void tss_activate(int pid);

// Windows whose title contains `filter` (NULL/"" = all windows).
int tss_window_count(int pid, const char *filter);
int tss_all_minimized(int pid, const char *filter);
AXUIElementRef tss_first_window(int pid, const char *filter); // retained
void tss_set_minimized(AXUIElementRef window, int minimized);
void tss_make_main(int pid, const char *filter);
int tss_window_titles(int pid, char *buf, int buflen);

void tss_enable_ax(int pid);
int tss_collect(int pid, const char *filter, TSSItem *out, int maxOut, int maxNodes, double maxSeconds);
int tss_press(AXUIElementRef ref);
void tss_release(AXUIElementRef ref);
void tss_post_escape(int pid);
