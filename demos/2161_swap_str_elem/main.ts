function main(): i32 {
  const a: string[] = ["x", "yy"]; 
  [a[0], a[1]] = [a[1], a[0]];
  console.log(a[0]);
  console.log(a[1]);
  return 0;
}
