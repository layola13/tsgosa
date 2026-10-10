function main(): i32 {
  let sum = 0;
  const a: i32[] = [5, 10, 15];
  for (let i = 0; i < a.length; i = i + 1) { sum = sum + a[i]; }
  console.log(sum);
  return 0;
}
