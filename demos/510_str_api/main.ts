function main(): i32 {
  const s = "hello";
  console.log(s.slice(1, 3));
  console.log(s.substring(1, 4));
  console.log(s.toUpperCase());
  console.log("  ab  ".trim());
  console.log(s.charCodeAt(1));
  console.log("abc".repeat(3));
  console.log("7".padStart(3, "0"));
  console.log(parseInt("42"));
  return 0;
}
