function main(): i32 {
  const a: i32[]|null = [1, 2, 3];
  console.log(a?.[1] ?? -1);
  const n: i32[]|null = null;
  console.log(n?.[0] ?? -1);
  console.log(a?.length ?? -1);
  return 0;
}
