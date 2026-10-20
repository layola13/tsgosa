export function main(): i32 {
  const xs = [10, 20];
  for (const k of xs.keys()) {
    console.log(k);
  }
  for (const v of xs.values()) {
    console.log(v);
  }
  return 0;
}
