"""Chart presentation only; domain and request accounting stay in visible SQL."""

from pathlib import Path

import matplotlib.pyplot as plt
import seaborn as sns


def theme():
    """Apply readable plotting defaults."""
    sns.set_theme(style="whitegrid", context="notebook", font_scale=1.05)
    plt.rcParams.update(
        {
            "figure.dpi": 130,
            "savefig.dpi": 150,
            "text.parse_math": False,
            "axes.spines.top": False,
            "axes.spines.right": False,
        }
    )


def finish(fig, name, title, subtitle):
    """Add provenance, save a review image and display."""
    fig.suptitle(title, x=0.1, y=0.98, ha="left", fontsize=17, fontweight="bold", color="#243e32")
    fig.text(0.1, 0.92, subtitle, ha="left", fontsize=11, color="#526b5b")
    fig.text(
        0.1,
        0.025,
        "Source: invented inventory and target counts · local read-only OData v4 fixture · three-entity pages\n"
        "vgi-odata v0.4.0 through Haybarn / VGI · no real enterprise account or inventory",
        fontsize=9,
        color="#526b5b",
    )
    fig.tight_layout(rect=(0.02, 0.12, 0.98, 0.79))
    folder = Path("validation/charts")
    folder.mkdir(parents=True, exist_ok=True)
    fig.savefig(folder / f"{name}.png", bbox_inches="tight")
    plt.show()
