function main(): i32 {
  console.log(`found ${"abc123".match(/[0-9]+/)[0] ?? "none"}!`);
  return 0;
}
