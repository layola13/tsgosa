function main(): i32 {
  const re = /[0-9]+/;
  console.log(re.exec("x7y")[0] ?? "miss");
  console.log(re.exec("xy")[0] ?? "miss");
  return 0;
}
