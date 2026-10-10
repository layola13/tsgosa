function main(): i32 {
  const re = /[0-9]+/;
  console.log(re.exec("abc")[0] ?? "miss");
  return 0;
}
