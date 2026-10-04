function isLower(s: string): i32 {
  const re = /^[a-z]+$/;
  if (re.test(s)) {
    return 1;
  }
  return 0;
}
function isDigits(s: string): i32 {
  const re2: RegExp = new RegExp("^[0-9]+$");
  if (re2.test(s)) {
    return 1;
  }
  return 0;
}
function main(): i32 {
  console.log(isLower("hello"));
  console.log(isLower("Hello123"));
  console.log(isDigits("abc123"));
  console.log(isDigits("456"));
  return 0;
}
