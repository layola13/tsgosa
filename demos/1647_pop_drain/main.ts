function main(): i32 {
  const a: i32[] = [5, 6];
  console.log(a.pop() ?? 0);
  console.log(a.pop() ?? 0);
  console.log(a.pop() ?? 0);
  return 0;
}
