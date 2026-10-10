function main(): i32 {
  const a: i32[] = [1, 2, 3, 4, 5, 6];
  console.log(a.filter((x) => x % 2 == 0).join(","));
  return 0;
}
