function main(): i32 {
  const strs: string[] = ["a", "bb", "ccc"];
  let total: i32 = 0;
  strs.forEach((s) => {
    total = total + s.length;
  });
  console.log(total);
  const n = strs.map((s) => s.length);
  console.log(n[0] + n[1] + n[2]);
  return 0;
}
