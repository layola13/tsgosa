export function main(): i32 {
  const xs = [10, 20];
  for (const [i, v] of xs.entries()) {
    console.log(i);
    console.log(v);
  }
  return 0;
}
