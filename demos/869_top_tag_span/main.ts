function wrap(parts: string[], x: i32): string {
  return parts[0];
}
function main(): i32 {
  console.log(wrap`a${41}b`);
  return 0;
}
