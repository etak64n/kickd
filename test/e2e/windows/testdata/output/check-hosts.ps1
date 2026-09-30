foreach ($i in 1..200) {
  [Console]::Out.WriteLine("host $i answers")
  [Console]::Error.WriteLine("host $i is slow")
}
