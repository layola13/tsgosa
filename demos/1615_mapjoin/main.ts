function main(): i32 {
  const a: i32[] = [1, 2, 3];
  console.log(a.map((x) => x * 2).join(","));
  return 0;
}
