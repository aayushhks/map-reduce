import sys


def parse_line(line):
    """Parse a line into a key and a sorted list of tuples."""
    key, value = line.strip().split(' ', 1)
    # Remove brackets and sort
    value = value.replace("[{", "").replace("}]", "")
    value_list = value.split("} {")
    tfidfs = set(value_list) # Put values into a set-of-strings
    return key, tfidfs

def compare_files(file1, file2):
    """Compare two files for equality, up to reordering of lists."""
    with open(file1, 'r') as f1, open(file2, 'r') as f2:
        lines1 = f1.readlines()
        lines2 = f2.readlines()

    if len(lines1) != len(lines2):
        return False
    
    parsed1 = {parse_line(line)[0]: parse_line(line)[1] for line in lines1}
    parsed2 = {parse_line(line)[0]: parse_line(line)[1] for line in lines2}

    return parsed1 == parsed2

# Pass two files as arguments.
file1 = sys.argv[1]
file2 = sys.argv[2]

if compare_files(file1, file2):
    print("PASS. The files are the same (up to reordering).")
    exit(0)
else:
    print("FAIL. The files are different.")
    exit(1)