from pwidget.cli import main


def test_main(capsys):
    main()
    assert "pwidget" in capsys.readouterr().out
