function main(): i32 {
  const a: i32[] = [1, 2, 3, 4];
  const r = a.map((x) => x + 1).filter((x) => x > 2);
  console.log(r.length);
  console.log(r[0]);
  return 0;
}
