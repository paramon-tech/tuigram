class Tuigram < Formula
  desc "Telegram client for the terminal with Vim key bindings"
  homepage "https://github.com/paramon-tech/tuigram"
  license "MIT"
  head "https://github.com/paramon-tech/tuigram.git", branch: "main"

  depends_on "go" => :build

  def install
    ENV["CGO_ENABLED"] = "0"
    ldflags = "-s -w -X main.version=HEAD"
    system "go", "build", *std_go_args(ldflags: ldflags), "./cmd/tuigram"
  end

  test do
    assert_match "tuigram", shell_output("#{bin}/tuigram --version")
    assert_match "TUIGRAM", shell_output("#{bin}/tuigram --demo --snapshot")
  end
end
