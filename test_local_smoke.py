import os


def test_default_data_dir_falls_back_when_container_path_is_unavailable(monkeypatch, tmp_path):
    from app import _select_data_dir

    monkeypatch.delenv("DATA_DIR", raising=False)
    monkeypatch.setattr(os, "makedirs", lambda path, exist_ok=False: (_ for _ in ()).throw(PermissionError(path)) if path == "/data" else None)
    monkeypatch.chdir(tmp_path)

    assert _select_data_dir() == str(tmp_path / "data")
