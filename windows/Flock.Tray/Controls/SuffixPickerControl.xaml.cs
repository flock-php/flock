using Flock.Kit;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Flock.Tray.Controls;

/// <summary>
///     The shared suffix picker: the domain menu (with an optional leading Inherit row) and the custom
///     field, with the daemon's live check running on the draft. The host pane puts its own Apply button
///     next to it, gates it on <see cref="CanApply" />, reads <see cref="SelectedTag" /> and
///     <see cref="DraftNormalized" />, and refreshes on <see cref="Changed" />. The semantics mirror the
///     macOS SuffixPicker and SuffixCheckModel.
/// </summary>
public sealed partial class SuffixPickerControl : UserControl
{
    /// <summary>The row tags that aren't suffixes (the macOS SuffixValue).</summary>
    public const string InheritTag = "inherit";

    public const string CustomTag = "custom";

    /// <summary>The domains the menu offers, as the daemon stores them: no leading dot.</summary>
    private static readonly string[] Presets = ["test", "loc", "internal", "alt", "example"];

    private const int DebounceMilliseconds = 300;

    private TldCheck? check;
    private CancellationTokenSource? pending;
    private uint checkSequence;
    private bool rebuilding;
    private bool seeded;

    public SuffixPickerControl()
    {
        InitializeComponent();
        Rebuild();
    }

    public static readonly DependencyProperty CurrentProperty = DependencyProperty.Register(
        nameof(Current),
        typeof(string),
        typeof(SuffixPickerControl),
        new PropertyMetadata("", (control, _) => ((SuffixPickerControl)control).Rebuild()));

    public static readonly DependencyProperty AllowInheritProperty = DependencyProperty.Register(
        nameof(AllowInherit),
        typeof(bool),
        typeof(SuffixPickerControl),
        new PropertyMetadata(false, (control, _) => ((SuffixPickerControl)control).Rebuild()));

    /// <summary>The suffix in the config today, without the leading dot (the daemon's convention): the
    /// Inherit row is labelled with it, and a verdict normalizing to it keeps Apply disabled.</summary>
    public string Current
    {
        get => (string)GetValue(CurrentProperty);
        set => SetValue(CurrentProperty, value);
    }

    /// <summary>Adds the leading "Inherit (.&lt;current&gt;)" row, for the pickers that sit under a global suffix.</summary>
    public bool AllowInherit
    {
        get => (bool)GetValue(AllowInheritProperty);
        set => SetValue(AllowInheritProperty, value);
    }

    /// <summary>The live verdict call the host wires (normally FlockClient.CheckTldAsync). While it is
    /// null the picker shows no verdicts: nothing is checked and Apply stays disabled here.</summary>
    public Func<string, Task<TldCheck>>? CheckAsync { get; set; }

    /// <summary>The tag of the picked row: <see cref="InheritTag" />, a preset, or <see cref="CustomTag" />;
    /// null while nothing is picked. The host decides its own Inherit rules with it.</summary>
    public string? SelectedTag => (Picker.SelectedItem as ComboBoxItem)?.Tag as string;

    /// <summary>True while the draft sits on the Inherit row: there is no suffix to check here, and the
    /// host computes its Apply itself (an `--inherit` run has no verdict to wait for).</summary>
    public bool IsInherit => SelectedTag == InheritTag;

    /// <summary>The domain a picked draft stands for: the daemon's normalized answer once the check
    /// passed, <see cref="InheritTag" /> while Inherit is picked; null with no usable draft.</summary>
    public string? DraftNormalized
        => IsInherit ? InheritTag : check is { Ok: true } verdict ? verdict.Normalized : null;

    /// <summary>Back to a clean picker (macOS reset): the applied domain is the config's, so the drafts
    /// and verdicts go and the rows re-seed onto <c>current</c>.</summary>
    public void Reset(string? current = null)
    {
        CustomBox.Text = "";
        seeded = false;
        rebuilding = true;
        Picker.SelectedItem = null;
        rebuilding = false;
        if (current is not null)
        {
            Current = current;
        }
        if (SelectedTag is null)
        {
            // Current didn't move (the DP callback stays silent), or it seeded nowhere: rebuild so the rows reseed.
            Rebuild();
        }
        SetCheck(null, null, "");
        Changed?.Invoke();
    }

    /// <summary>Apply is enabled only on a checked, acceptable domain the config doesn't already have
    /// (macOS canApply). With Inherit picked this is false by design: the host owns that button's state.</summary>
    public bool CanApply => !IsInherit && check is { Ok: true } verdict && verdict.Normalized != Current;

    /// <summary>Raised whenever the draft or the verdict changed, so the host refreshes its Apply button.</summary>
    public event Action? Changed;

    /// <summary>Starts the picker on a row (the add-domain sheet opens on Inherit; a site row opens on its suffix).</summary>
    public void Select(string tag)
    {
        var item = FindItem(tag);
        if (item is not null)
        {
            Picker.SelectedItem = item;
        }
    }

    /// <summary>The domain to check and apply: the picked preset, else the trimmed custom text (macOS candidate).</summary>
    private string Candidate()
        => SelectedTag switch
        {
            null or InheritTag => "",
            CustomTag => CustomBox.Text.Trim(),
            var preset => preset,
        };

    private void PickerChanged(object sender, SelectionChangedEventArgs args)
    {
        if (rebuilding)
        {
            return;
        }
        ApplyCustomVisibility();
        ScheduleCheck();
        Changed?.Invoke();
    }

    private void CustomChanged(object sender, TextChangedEventArgs args)
    {
        if (rebuilding)
        {
            return;
        }
        ScheduleCheck();
        Changed?.Invoke();
    }

    /// <summary>Rebuilds the rows (the Inherit label follows Current), keeping the picked row where possible.</summary>
    private void Rebuild()
    {
        var keep = SelectedTag;
        rebuilding = true;
        Picker.Items.Clear();
        if (AllowInherit)
        {
            Picker.Items.Add(new ComboBoxItem { Content = $"Inherit (.{Current})", Tag = InheritTag });
        }
        foreach (var preset in Presets)
        {
            Picker.Items.Add(new ComboBoxItem { Content = $".{preset}", Tag = preset });
        }
        Picker.Items.Add(new ComboBoxItem { Content = "Custom…", Tag = CustomTag });
        var picked = FindItem(keep) ?? FindItem(SeedTag());
        if (picked is not null)
        {
            Picker.SelectedItem = picked;
        }
        rebuilding = false;
        ApplyCustomVisibility();
        ScheduleCheck();
    }

    /// <summary>A domain the menu doesn't offer sits in the custom field to be edited, and an untouched
    /// picker opens on the preset of the current domain (the macOS selection()/prefill(), done once).</summary>
    private string? SeedTag()
    {
        if (seeded || Current.Length == 0)
        {
            return null;
        }
        seeded = true;
        if (Presets.Contains(Current))
        {
            return Current;
        }
        CustomBox.Text = Current;
        return CustomTag;
    }

    private object? FindItem(string? tag) =>
        tag is null ? null : Picker.Items.OfType<ComboBoxItem>().FirstOrDefault(item => (item.Tag as string) == tag);

    private void ApplyCustomVisibility()
    {
        CustomBox.Visibility = SelectedTag == CustomTag ? Visibility.Visible : Visibility.Collapsed;
    }

    /// <summary>Restarts the debounce: a newer candidate cancels the pending run, and its late verdict is
    /// dropped by <see cref="checkSequence" /> (the macOS .task(id:) restart discipline).</summary>
    private void ScheduleCheck()
    {
        pending?.Cancel();
        pending = null;
        var sequence = ++checkSequence;
        var candidate = Candidate();
        if (candidate.Length == 0)
        {
            // Nothing drafted (Inherit, or an emptied field): no verdict to show.
            SetCheck(null, null, candidate);
            return;
        }
        var wait = new CancellationTokenSource();
        pending = wait;
        _ = CheckDebouncedAsync(sequence, candidate, wait);
    }

    private async Task CheckDebouncedAsync(uint sequence, string candidate, CancellationTokenSource wait)
    {
        try
        {
            await Task.Delay(TimeSpan.FromMilliseconds(DebounceMilliseconds), wait.Token);
        }
        catch (OperationCanceledException)
        {
            return;
        }
        if (sequence != checkSequence || CheckAsync is not { } checkTld)
        {
            return;
        }
        TldCheck verdict;
        try
        {
            verdict = await checkTld(candidate);
        }
        catch (Exception)
        {
            // The daemon didn't answer; the last verdict stays on screen, as on macOS.
            if (sequence != checkSequence)
            {
                return;
            }
            NoteLine.Text = check is null
                ? "Couldn’t check with the daemon; Apply stays disabled until the daemon answers."
                : "Couldn’t check with the daemon; the verdict shown is the last one.";
            NoteLine.Visibility = Visibility.Visible;
            Changed?.Invoke();
            return;
        }
        if (sequence != checkSequence)
        {
            return;
        }
        SetCheck(verdict, null, candidate);
        Changed?.Invoke();
    }

    /// <summary>Draws the verdict lines (the macOS SuffixFeedback): the normalized suffix when the daemon
    /// changed it, its warning, its error, and why a check couldn't run.</summary>
    private void SetCheck(TldCheck? verdict, string? note, string candidate)
    {
        check = verdict;
        NoteLine.Text = note ?? "";
        if (verdict is null)
        {
            NormalizedLine.Text = "";
            WarningLine.Text = "";
            ErrorLine.Text = "";
        }
        else
        {
            NormalizedLine.Text = verdict.Ok && verdict.Normalized.Length > 0 && verdict.Normalized != candidate
                ? $"→ .{verdict.Normalized}"
                : "";
            WarningLine.Text = verdict.Warning ?? "";
            ErrorLine.Text = verdict.Error ?? "";
        }
        foreach (var line in new[] { NormalizedLine, WarningLine, ErrorLine, NoteLine })
        {
            line.Visibility = line.Text.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        }
    }
}
