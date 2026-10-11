function main(): i32 {
  const ss = [["x"]];
  const [[s = "d"]] = ss;
  console.log(s);
  return 0;
}
