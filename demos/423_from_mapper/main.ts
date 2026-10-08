function main(): i32 {
  const b = Array.from([1, 2], (x) => x * 2);
  console.log(b[1]);
  const c = Array.from({length: 3}, (_, i) => i * 10);
  console.log(c[2]);
  return 0;
}
