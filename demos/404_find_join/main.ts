function main(): i32 {
  const a: i32[] = [1, 2, 3];
  const r: i32 = a.find((x) => x > 1) ?? 0;
  console.log(r);
  console.log(a.join(",").length);
  return 0;
}
