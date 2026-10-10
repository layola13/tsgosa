function main(): i32 {
  const re = /[0-9]+/;
  const s: string = "abc123";
  console.log(re.test(s) ? 1 : 0);
  return 0;
}
